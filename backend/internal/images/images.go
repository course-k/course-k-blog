package images

import (
	"bytes"
	"context"
	"encoding/binary"
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
	"golang.org/x/image/webp"
)

// MaxUploadBytes bounds both the uploaded file and the normalized stored image
// (shown to the owner as 10MB).
const MaxUploadBytes = 10_000_000
const maxPixels = 12_000_000

var ErrInvalid = errors.New("invalid image")

// Rejection explains which documented limit an image failed. It matches ErrInvalid.
type Rejection struct{ Code string }

func (r Rejection) Error() string        { return "invalid image: " + r.Code }
func (r Rejection) Is(target error) bool { return target == ErrInvalid }

var (
	ErrEmpty           = Rejection{"empty"}
	ErrTooLarge        = Rejection{"too_large"}
	ErrFormat          = Rejection{"format"}
	ErrPixels          = Rejection{"pixels"}
	ErrAnimated        = Rejection{"animated"}
	ErrCorrupt         = Rejection{"corrupt"}
	ErrEncodedTooLarge = Rejection{"encoded_too_large"}
)
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
// PNG and WebP are stored as PNG (keeping transparency); JPEG stays JPEG at
// quality 85. The real format is detected from the bytes, never the file name.
func Sanitize(input []byte) ([]byte, string, error) {
	if len(input) == 0 {
		return nil, "", ErrEmpty
	}
	if len(input) > MaxUploadBytes {
		return nil, "", ErrTooLarge
	}
	format := detectFormat(input)
	if format == "" {
		return nil, "", ErrFormat
	}
	if animated(format, input) {
		return nil, "", ErrAnimated
	}
	var config image.Config
	var err error
	switch format {
	case "png":
		config, err = png.DecodeConfig(bytes.NewReader(input))
	case "jpeg":
		config, err = jpeg.DecodeConfig(bytes.NewReader(input))
	case "webp":
		config, err = webp.DecodeConfig(bytes.NewReader(input))
	}
	if err != nil || config.Width < 1 || config.Height < 1 {
		return nil, "", ErrCorrupt
	}
	if int64(config.Width)*int64(config.Height) > maxPixels {
		return nil, "", ErrPixels
	}
	var picture image.Image
	switch format {
	case "png":
		picture, err = png.Decode(bytes.NewReader(input))
	case "jpeg":
		picture, err = jpeg.Decode(bytes.NewReader(input))
	case "webp":
		picture, err = webp.Decode(bytes.NewReader(input))
	}
	if err != nil {
		return nil, "", ErrCorrupt
	}
	var output bytes.Buffer
	mime := "image/png"
	if format == "jpeg" {
		mime = "image/jpeg"
		err = jpeg.Encode(&output, picture, &jpeg.Options{Quality: 85})
	} else {
		err = png.Encode(&output, picture)
	}
	if err != nil {
		return nil, "", ErrCorrupt
	}
	if output.Len() > MaxUploadBytes {
		return nil, "", ErrEncodedTooLarge
	}
	return output.Bytes(), mime, nil
}

// StoredMIME is the normalized MIME type that Sanitize produces for input bytes,
// or "" when the format is unsupported. It lets callers fix the final object key
// before the slower decode starts.
func StoredMIME(input []byte) string {
	switch detectFormat(input) {
	case "jpeg":
		return "image/jpeg"
	case "png", "webp":
		return "image/png"
	}
	return ""
}

func detectFormat(input []byte) string {
	switch {
	case bytes.HasPrefix(input, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case bytes.HasPrefix(input, []byte{0xff, 0xd8, 0xff}):
		return "jpeg"
	case len(input) >= 12 && string(input[:4]) == "RIFF" && string(input[8:12]) == "WEBP":
		return "webp"
	}
	return ""
}

// animated reports APNG (acTL before image data) and animated WebP.
func animated(format string, input []byte) bool {
	switch format {
	case "png":
		for offset := 8; offset+8 <= len(input); {
			length := int(binary.BigEndian.Uint32(input[offset:]))
			kind := string(input[offset+4 : offset+8])
			if kind == "acTL" {
				return true
			}
			if kind == "IDAT" || length < 0 || offset+12+length > len(input) {
				return false
			}
			offset += 12 + length
		}
	case "webp":
		for offset := 12; offset+8 <= len(input); {
			kind := string(input[offset : offset+4])
			length := int(binary.LittleEndian.Uint32(input[offset+4:]))
			if kind == "ANIM" || kind == "ANMF" {
				return true
			}
			if kind == "VP8X" && offset+9 <= len(input) && input[offset+8]&0x02 != 0 {
				return true
			}
			if length < 0 || offset+8+length > len(input) {
				return false
			}
			offset += 8 + length + length%2
		}
	}
	return false
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

// Message explains a rejection with the documented format, size and pixel limits.
func Message(code string) string {
	switch code {
	case ErrEmpty.Code:
		return "0バイトの画像は使えません。"
	case ErrTooLarge.Code:
		return "画像は1枚10MB（10,000,000バイト）以下にしてください。"
	case ErrFormat.Code:
		return "PNG・JPEG・WebPの画像を使えます。SVG・GIF・HEICは対象外です。"
	case ErrPixels.Code:
		return "画像は1200万画素以下にしてください。"
	case ErrAnimated.Code:
		return "アニメーション画像は使えません。"
	case ErrEncodedTooLarge.Code:
		return "変換後の画像が10MBを超えるため登録できません。画像を縮小してください。"
	case "size_mismatch", "checksum_mismatch":
		return "送信された画像の内容を確認できませんでした。再試行してください。"
	}
	return "画像を読み取れませんでした。PNG・JPEG・WebP、10MB以下、1200万画素以下の画像を使えます。"
}
