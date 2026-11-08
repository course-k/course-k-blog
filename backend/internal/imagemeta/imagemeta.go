// Package imagemeta validates the reserved display setting stored in the
// standard title of a Markdown image, e.g.
//
//	![alt](/api/images/ID "blog-image:v1;width=50;align=left")
//
// The frontend owns rendering; the server only rejects malformed values that
// use the reserved prefix. Ordinary image titles are left untouched.
package imagemeta

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Prefix marks a title that carries display settings instead of a tooltip.
const Prefix = "blog-image:"

var ErrInvalid = errors.New("invalid image display setting")

var (
	widthPattern = regexp.MustCompile(`^(auto|[1-9][0-9]|100)$`)
	// encodeURIComponent output: unreserved characters and percent escapes only.
	titlePattern = regexp.MustCompile(`^(?:[A-Za-z0-9\-_.!~*'()]|%[0-9A-Fa-f]{2})+$`)
)

type Display struct {
	// Width is a percentage of the content width; 0 means automatic.
	Width int
	Align string
	Title string
}

// Parse accepts exactly the v1 grammar, with keys in the documented order:
// blog-image:v1;width=<auto|10-100>;align=<left|center|right>[;title=<encoded>]
func Parse(title string) (Display, error) {
	rest, ok := strings.CutPrefix(title, Prefix+"v1;")
	if !ok {
		return Display{}, ErrInvalid
	}
	parts := strings.Split(rest, ";")
	if len(parts) != 2 && len(parts) != 3 {
		return Display{}, ErrInvalid
	}
	width, ok := strings.CutPrefix(parts[0], "width=")
	if !ok || !widthPattern.MatchString(width) {
		return Display{}, ErrInvalid
	}
	var display Display
	if width != "auto" {
		display.Width, _ = strconv.Atoi(width)
	}
	align, ok := strings.CutPrefix(parts[1], "align=")
	if !ok || (align != "left" && align != "center" && align != "right") {
		return Display{}, ErrInvalid
	}
	display.Align = align
	if len(parts) == 3 {
		encoded, ok := strings.CutPrefix(parts[2], "title=")
		if !ok || !titlePattern.MatchString(encoded) {
			return Display{}, ErrInvalid
		}
		decoded, err := url.PathUnescape(encoded)
		if err != nil || !utf8.ValidString(decoded) {
			return Display{}, ErrInvalid
		}
		display.Title = decoded
	}
	return display, nil
}

// Validate checks every image (inline or reference) parsed as CommonMark.
// Code blocks, inline code and link text that merely look like images are not
// images in the syntax tree and are therefore ignored.
func Validate(markdown string) error {
	source := []byte(markdown)
	document := goldmark.DefaultParser().Parse(text.NewReader(source))
	return ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		picture, ok := node.(*ast.Image)
		if !ok {
			return ast.WalkContinue, nil
		}
		title := string(picture.Title)
		if !strings.HasPrefix(title, Prefix) {
			return ast.WalkContinue, nil
		}
		if _, err := Parse(title); err != nil {
			return ast.WalkStop, err
		}
		return ast.WalkContinue, nil
	})
}
