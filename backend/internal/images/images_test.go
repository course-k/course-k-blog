package images

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestSanitizeImage(t *testing.T) {
	original := image.NewRGBA(image.Rect(0, 0, 2, 2))
	original.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var input bytes.Buffer
	if err := png.Encode(&input, original); err != nil {
		t.Fatal(err)
	}
	body, mime, err := Sanitize(input.Bytes())
	if err != nil || mime != "image/png" || len(body) == 0 {
		t.Fatal("PNG rejected", err)
	}
	for _, body := range [][]byte{[]byte(`<svg onload="alert(1)"/>`), []byte("not an image"), bytes.Repeat([]byte{0}, MaxUploadBytes+1)} {
		if _, _, err := Sanitize(body); err == nil {
			t.Fatal("unsafe or oversized image accepted")
		}
	}
}
func TestMarkdownImageReferences(t *testing.T) {
	id := strings.Repeat("a", 43)
	text := "![inline](/api/images/" + id + ")\n\n![reference][img]\n\n[img]: /api/images/" + id + "\n\n```md\n![not a reference](/api/images/" + strings.Repeat("b", 43) + ")\n```"
	refs, err := References(text)
	if err != nil || len(refs) != 1 || refs[0] != id {
		t.Fatalf("refs=%v err=%v", refs, err)
	}
	if _, err := References("![invalid](/api/images/../private)"); err == nil {
		t.Fatal("invalid local reference accepted")
	}
}

func TestMaximumPixelImageIsAcceptedAndLargerImageRejected(t *testing.T) {
	for _, tc := range []struct {
		name   string
		height int
		valid  bool
	}{{"12 megapixels", 3000, true}, {"over 12 megapixels", 3001, false}} {
		t.Run(tc.name, func(t *testing.T) {
			var input bytes.Buffer
			if err := png.Encode(&input, image.NewRGBA(image.Rect(0, 0, 4000, tc.height))); err != nil {
				t.Fatal(err)
			}
			body, mime, err := Sanitize(input.Bytes())
			if (err == nil) != tc.valid {
				t.Fatalf("accepted=%v, expected=%v", err == nil, tc.valid)
			}
			if tc.valid {
				config, format, err := image.DecodeConfig(bytes.NewReader(body))
				if err != nil || format != "png" || mime != "image/png" || config.Width != 4000 || config.Height != 3000 {
					t.Fatal("maximum image content changed", err)
				}
			}
		})
	}
}

func TestWebPIsNormalizedToPNGWithTransparency(t *testing.T) {
	for _, name := range []string{"alpha.webp", "lossy.webp"} {
		input, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		body, mime, err := Sanitize(input)
		if err != nil || mime != "image/png" {
			t.Fatalf("%s: mime=%s err=%v", name, mime, err)
		}
		picture, err := png.Decode(bytes.NewReader(body))
		if err != nil || picture.Bounds().Dx() != 3 || picture.Bounds().Dy() != 2 {
			t.Fatalf("%s: decoded %v %v", name, picture, err)
		}
		if name == "alpha.webp" {
			if _, _, _, alpha := picture.At(1, 0).RGBA(); alpha != 0 {
				t.Fatalf("transparency lost: %d", alpha)
			}
		}
	}
}

func TestRejectionsNameTheLimit(t *testing.T) {
	extended, err := os.ReadFile("testdata/alpha-extended.webp")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Sanitize(extended); err != nil {
		t.Fatal("still extended WebP rejected", err)
	}
	// Set the VP8X animation flag; animated WebP must be rejected, not reduced to one frame.
	animatedWebP := append([]byte{}, extended...)
	animatedWebP[20] |= 0x02
	var still bytes.Buffer
	if err := png.Encode(&still, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	// Insert an acTL chunk before IDAT: an APNG must not pass as its first frame.
	apng := append([]byte{}, still.Bytes()[:33]...)
	apng = append(apng, 0, 0, 0, 8, 'a', 'c', 'T', 'L', 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0)
	apng = append(apng, still.Bytes()[33:]...)
	var gif bytes.Buffer
	gif.WriteString("GIF89a")
	cases := map[string]struct {
		input []byte
		want  error
	}{
		"empty":              {nil, ErrEmpty},
		"over 10,000,000":    {append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, MaxUploadBytes-7)...), ErrTooLarge},
		"svg":                {[]byte(`<svg onload="alert(1)"/>`), ErrFormat},
		"gif":                {gif.Bytes(), ErrFormat},
		"animated webp":      {animatedWebP, ErrAnimated},
		"apng":               {apng, ErrAnimated},
		"truncated png":      {still.Bytes()[:20], ErrCorrupt},
		"jpeg header only":   {[]byte{0xff, 0xd8, 0xff, 0xe0, 0, 0}, ErrCorrupt},
		"png renamed as jpg": {still.Bytes(), nil},
	}
	for name, tc := range cases {
		_, mime, err := Sanitize(tc.input)
		if tc.want == nil {
			if err != nil || mime != "image/png" {
				t.Fatalf("%s: %s %v", name, mime, err)
			}
			continue
		}
		if !errors.Is(err, tc.want) || !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: got %v want %v", name, err, tc.want)
		}
	}
}

func TestUploadBoundaryAndJPEGQuality(t *testing.T) {
	var input bytes.Buffer
	if err := jpeg.Encode(&input, image.NewRGBA(image.Rect(0, 0, 8, 8)), &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	// Pad with trailing bytes ignored by decoders to reach exactly the limit.
	exact := append(append([]byte{}, input.Bytes()...), make([]byte, MaxUploadBytes-input.Len())...)
	if len(exact) != 10_000_000 {
		t.Fatal("fixture size", len(exact))
	}
	body, mime, err := Sanitize(exact)
	if err != nil || mime != "image/jpeg" || len(body) > MaxUploadBytes {
		t.Fatalf("10,000,000-byte JPEG: %v", err)
	}
	if _, _, err := Sanitize(append(exact, 0)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("10,000,001 bytes: %v", err)
	}
}

func TestStoredMIMEUsesBytesNotNames(t *testing.T) {
	webpBytes, _ := os.ReadFile("testdata/alpha.webp")
	if StoredMIME(webpBytes) != "image/png" || StoredMIME([]byte{0xff, 0xd8, 0xff}) != "image/jpeg" || StoredMIME([]byte("GIF89a")) != "" {
		t.Fatal("stored MIME detection")
	}
}
