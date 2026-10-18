package images

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

const MaxUploadBytes = 5 * 1024 * 1024
const maxPixels = 12_000_000

var ErrInvalid = errors.New("invalid image")
var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func ValidID(id string) bool { return validID.MatchString(id) }

type Image struct {
	ID, Key, MIME, SHA256 string
	Size                  int
	CreatedAt             time.Time
}
type Repository interface {
	SaveImage(context.Context, Image) error
	GetImage(context.Context, string) (Image, error)
	ImagePublic(context.Context, string) (bool, error)
}
type Blobs interface {
	Put(context.Context, string, []byte, string) error
	Get(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
}
type Service struct {
	Repository Repository
	Blobs      Blobs
}

// Decode and re-encode supported images to drop EXIF and unrelated embedded data.
func Sanitize(input []byte) ([]byte, string, error) {
	if len(input) == 0 || len(input) > MaxUploadBytes {
		return nil, "", ErrInvalid
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(input))
	if err != nil || (format != "png" && format != "jpeg") || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > maxPixels {
		return nil, "", ErrInvalid
	}
	picture, _, err := image.Decode(bytes.NewReader(input))
	if err != nil {
		return nil, "", ErrInvalid
	}
	var output bytes.Buffer
	mime := "image/png"
	if format == "jpeg" {
		mime = "image/jpeg"
		err = jpeg.Encode(&output, picture, &jpeg.Options{Quality: 85})
	} else {
		err = png.Encode(&output, picture)
	}
	if err != nil || output.Len() > MaxUploadBytes {
		return nil, "", ErrInvalid
	}
	return output.Bytes(), mime, nil
}

// CommonMark parsing includes reference-style images and excludes fenced code.
func References(markdown string) ([]string, error) {
	document := goldmark.DefaultParser().Parse(text.NewReader([]byte(markdown)))
	ids := map[string]bool{}
	err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		picture, ok := node.(*ast.Image)
		if !ok {
			return ast.WalkContinue, nil
		}
		destination := string(picture.Destination)
		if !strings.HasPrefix(destination, "/api/images/") {
			return ast.WalkContinue, nil
		}
		id := strings.TrimPrefix(destination, "/api/images/")
		if !ValidID(id) {
			return ast.WalkStop, ErrInvalid
		}
		ids[id] = true
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}
