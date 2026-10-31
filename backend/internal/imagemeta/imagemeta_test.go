package imagemeta

import (
	"errors"
	"testing"
)

func TestParseGrammar(t *testing.T) {
	valid := map[string]Display{
		"blog-image:v1;width=auto;align=left":                   {Align: "left"},
		"blog-image:v1;width=10;align=center":                   {Width: 10, Align: "center"},
		"blog-image:v1;width=100;align=right":                   {Width: 100, Align: "right"},
		"blog-image:v1;width=55;align=left;title=%E5%9B%B3%201": {Width: 55, Align: "left", Title: "図 1"},
		"blog-image:v1;width=55;align=left;title=it's(ok)":      {Width: 55, Align: "left", Title: "it's(ok)"},
	}
	for input, want := range valid {
		got, err := Parse(input)
		if err != nil || got != want {
			t.Fatalf("%q: got %+v, %v", input, got, err)
		}
	}
	invalid := []string{
		"blog-image:v2;width=50;align=left",
		"blog-image:v1;width=9;align=left",
		"blog-image:v1;width=101;align=left",
		"blog-image:v1;width=050;align=left",
		"blog-image:v1;width=+50;align=left",
		"blog-image:v1;align=left;width=50",
		"blog-image:v1;width=50",
		"blog-image:v1;width=50;align=justify",
		"blog-image:v1;width=50;align=left;title=",
		"blog-image:v1;width=50;align=left;title=a b",
		"blog-image:v1;width=50;align=left;title=%ZZ",
		"blog-image:v1;width=50;align=left;title=%FF",
		"blog-image:v1;width=50;align=left;caption=x",
		"blog-image:v1;width=50;align=left;title=a;extra=1",
		"blog-image:v1;width=50;align=left;",
	}
	for _, input := range invalid {
		if _, err := Parse(input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%q accepted", input)
		}
	}
}

func TestValidateOnlyReservedImageTitles(t *testing.T) {
	ok := []string{
		"![a](/x.png)",
		"![a](/x.png \"plain tooltip\")",
		"![a](/x.png \"blog-image:v1;width=40;align=right\")",
		"```\n![a](/x.png \"blog-image:broken\")\n```",
		"`![a](/x.png \"blog-image:broken\")`",
		"[![a](/x.png \"blog-image:v1;width=auto;align=left\")](https://example.com)",
		"![a][r]\n\n[r]: /x.png \"blog-image:v1;width=30;align=center\"",
	}
	for _, markdown := range ok {
		if err := Validate(markdown); err != nil {
			t.Fatalf("%q rejected: %v", markdown, err)
		}
	}
	bad := []string{
		"![a](/x.png \"blog-image:v1;width=5;align=left\")",
		"text\n\n![a][r]\n\n[r]: /x.png \"blog-image:nope\"",
		"![a](/x.png 'blog-image:v1;width=50;align=middle')",
	}
	for _, markdown := range bad {
		if !errors.Is(Validate(markdown), ErrInvalid) {
			t.Fatalf("%q accepted", markdown)
		}
	}
}
