package images

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
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
