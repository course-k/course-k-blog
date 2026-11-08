// Package releasecheck exercises the public production path from a trusted ECS
// task. Its short-lived session never leaves process memory or reaches logs.
package releasecheck

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/blog"
	"coursek/blog/backend/internal/external"
	"coursek/blog/backend/internal/images"
)

const Origin = "https://blog.course-k.com"

type Store interface {
	PutSession(context.Context, blog.Session) error
	DeleteSession(context.Context, string) error
	SchemaVersion(context.Context) (int, error)
	VerifySchema(context.Context) error
	CleanupReleaseCheck(context.Context, string) ([]string, error)
}
type Check struct {
	Store      Store
	Client     *http.Client
	Bucket     string
	Backup     func(context.Context) error
	DeleteBlob func(context.Context, string) error
	Sync       bool
	Schema     int
}

func boundaryPNG() ([]byte, error) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		return nil, err
	}
	// PNG permits trailing bytes. The transport must carry exactly the advertised
	// limit; the server decodes and re-encodes it into a small ordinary PNG.
	return append(b.Bytes(), make([]byte, 10_000_000-b.Len())...), nil
}
func webP() []byte {
	b, _ := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4TBYAAAAvAkAAEA8Q8x+DHAwFaRuwsN2J6H88")
	return b
}
func (c Check) Run(ctx context.Context) (err error) {
	if c.Store == nil || c.Backup == nil || c.DeleteBlob == nil || !strings.HasPrefix(c.Bucket, "course-k-blog-production-imagesbucket-") {
		return errors.New("production check configuration required")
	}
	if c.Client == nil {
		c.Client = &http.Client{Timeout: 40 * time.Second}
	}
	// Never follow redirects while holding an owner session or signed upload.
	client := *c.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if e := c.Store.VerifySchema(ctx); e != nil {
		return errors.New("production schema checksum verification failed")
	}
	version, e := c.Store.SchemaVersion(ctx)
	if e != nil || version != c.Schema || c.Schema < 1 {
		return errors.New("expected production schema required")
	}
	token, e := auth.Token()
	if e != nil {
		return errors.New("check identity unavailable")
	}
	proof, e := auth.Token()
	if e != nil {
		return errors.New("check fixture unavailable")
	}
	hash := auth.Digest(token)
	if e = c.Store.PutSession(ctx, blog.Session{Hash: hash, ExpiresAt: time.Now().Add(8 * time.Minute)}); e != nil {
		return errors.New("check session unavailable")
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 60*time.Second)
		defer done()
		// The repository only removes records belonging to this random creation key.
		keys, cleanupErr := c.Store.CleanupReleaseCheck(cleanup, proof)
		for _, key := range keys {
			cleanupErr = errors.Join(cleanupErr, c.DeleteBlob(cleanup, key))
		}
		cleanupErr = errors.Join(cleanupErr, c.Store.DeleteSession(cleanup, hash))
		if cleanupErr != nil {
			err = errors.Join(err, errors.New("check fixture cleanup failed"))
		}
		if cleanupErr == nil {
			if e := c.Backup(cleanup); e != nil {
				err = errors.Join(err, errors.New("post-check backup failed"))
			}
		}
	}()
	request := func(method, path string, input, output any, owner bool, want int) error {
		var body io.Reader
		if input != nil {
			b, e := json.Marshal(input)
			if e != nil {
				return errors.New("check request invalid")
			}
			body = bytes.NewReader(b)
		}
		r, e := http.NewRequestWithContext(ctx, method, Origin+path, body)
		if e != nil {
			return errors.New("check URL invalid")
		}
		r.Header.Set("Origin", Origin)
		if input != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		if owner {
			r.AddCookie(&http.Cookie{Name: "coursek_session", Value: token})
		}
		resp, e := client.Do(r)
		if e != nil {
			return fmt.Errorf("production request failed: %s", path)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			return fmt.Errorf("production status mismatch: %s expected %d got %d", path, want, resp.StatusCode)
		}
		if output != nil && json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(output) != nil {
			return fmt.Errorf("production response invalid: %s", path)
		}
		return nil
	}
	if e = request("GET", "/api/admin/posts", nil, nil, false, 401); e != nil {
		return e
	}
	var post blog.Post
	title := "CI production check " + proof[:12]
	markdown := "<!-- course-k-release:" + proof + " -->\nProduction release verification."
	if e = request("POST", "/api/admin/posts", map[string]any{"title": title, "markdown": markdown, "creationKey": proof}, &post, true, 201); e != nil {
		return e
	}
	if !auth.ValidToken(post.ID) {
		return errors.New("check article identity invalid")
	}
	var urls []string
	for i, mime := range []string{"image/png", "image/webp"} {
		data := webP()
		if i == 0 {
			data, e = boundaryPNG()
			if e != nil {
				return errors.New("check image unavailable")
			}
		}
		sum := sha256.Sum256(data)
		var upload struct {
			UploadID string         `json:"uploadId"`
			Status   string         `json:"status"`
			Upload   *images.Target `json:"upload"`
			Result   *images.Result `json:"result"`
		}
		if e = request("POST", "/api/admin/image-uploads", images.UploadRequest{ClientImageID: "release-" + auth.Digest(proof)[:48] + fmt.Sprint(i), FileName: "release-check", ContentType: mime, ByteSize: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}, &upload, true, 200); e != nil {
			return e
		}
		if upload.Upload == nil || upload.Upload.Method != "POST" {
			return errors.New("signed S3 POST required")
		}
		target, e := url.Parse(upload.Upload.URL)
		if e != nil || target.Scheme != "https" || target.Host != c.Bucket+".s3.us-west-2.amazonaws.com" || target.User != nil {
			return errors.New("unexpected image upload destination")
		}
		var form bytes.Buffer
		writer := multipart.NewWriter(&form)
		for k, v := range upload.Upload.Fields {
			if writer.WriteField(k, v) != nil {
				return errors.New("upload field invalid")
			}
		}
		part, e := writer.CreateFormFile("file", "release-check")
		if e != nil {
			return errors.New("upload file invalid")
		}
		if _, e = part.Write(data); e != nil {
			return errors.New("upload bytes invalid")
		}
		writer.Close()
		r, e := http.NewRequestWithContext(ctx, "POST", target.String(), &form)
		if e != nil {
			return errors.New("upload request invalid")
		}
		r.Header.Set("Content-Type", writer.FormDataContentType())
		r.Header.Set("Origin", Origin)
		resp, e := client.Do(r)
		if e != nil {
			return errors.New("real S3 upload failed")
		}
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 || resp.Header.Get("Access-Control-Allow-Origin") != Origin {
			return errors.New("real S3 upload or CORS failed")
		}
		upload.Upload = nil
		if e = request("POST", "/api/admin/image-uploads/"+upload.UploadID+"/complete", struct{}{}, &upload, true, 200); e != nil {
			return e
		}
		if upload.Status != "completed" || upload.Result == nil || !auth.ValidToken(upload.Result.ID) || upload.Result.URL != "/api/images/"+upload.Result.ID {
			return errors.New("image registration incomplete")
		}
		urls = append(urls, upload.Result.URL)
	}
	markdown += "\n\n![PNG](" + urls[0] + ")\n\n![WebP](" + urls[1] + ")\n"
	path := "/api/admin/posts/" + post.ID
	if e = request("PUT", path, map[string]any{"title": title, "markdown": markdown, "version": post.Version}, &post, true, 200); e != nil {
		return e
	}
	var saved blog.Post
	if e = request("GET", path, nil, &saved, true, 200); e != nil {
		return e
	}
	if saved.Markdown != markdown {
		return errors.New("saved content differs")
	}
	for _, p := range urls {
		if e = request("GET", p, nil, nil, false, 404); e != nil {
			return e
		}
	}
	if e = request("POST", path+"/publish", map[string]int64{"version": saved.Version}, &post, true, 200); e != nil {
		return e
	}
	for _, p := range urls {
		if e = request("GET", p, nil, nil, false, 200); e != nil {
			return e
		}
	}
	if e = request("POST", path+"/unpublish", map[string]int64{"version": post.Version}, &post, true, 200); e != nil {
		return e
	}
	for _, p := range urls {
		if e = request("GET", p, nil, nil, false, 404); e != nil {
			return e
		}
	}
	var states []external.SyncState
	if e = request("GET", "/api/admin/sync", nil, &states, true, 200); e != nil {
		return e
	}
	if len(states) != 3 {
		return errors.New("sync sources unavailable")
	}
	if c.Sync {
		// Only the explicitly requested special release exercises an actual sync.
		if e = request("POST", "/api/admin/sync/qiita", struct{}{}, nil, true, 202); e != nil {
			return e
		}
		finished := false
		for i := 0; i < 40; i++ {
			if e = request("GET", "/api/admin/sync", nil, &states, true, 200); e != nil {
				return e
			}
			for _, s := range states {
				if s.Source == "qiita" && !s.Requested && (s.Status == "success" || s.Status == "partial") {
					finished = true
				}
				if s.Source == "qiita" && s.Status == "failed" {
					return errors.New("explicit sync failed")
				}
			}
			if finished {
				break
			}
			select {
			case <-ctx.Done():
				return errors.New("sync check expired")
			case <-time.After(3 * time.Second):
			}
		}
		if !finished {
			return errors.New("sync check timed out")
		}
	}
	return nil
}
