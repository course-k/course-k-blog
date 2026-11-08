package blog

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateBoundaries(t *testing.T) {
	cases := []struct {
		name, title, markdown string
		valid                 bool
	}{
		{"empty title saved as draft", "", "body", true},
		{"blank title saved as draft", " \n\t", "body", true},
		{"reserved image setting", "", "![a](/api/images/x \"blog-image:v1;width=50;align=center\")", true},
		{"invalid reserved image setting", "", "![a](/api/images/x \"blog-image:v1;width=5;align=left\")", false},
		{"200 ASCII code points", strings.Repeat("a", 200), "", true},
		{"201 ASCII code points", strings.Repeat("a", 201), "", false},
		{"200 Unicode code points", strings.Repeat("猫", 200), "", true},
		{"201 Unicode code points", strings.Repeat("猫", 201), "", false},
		{"invalid title UTF-8", string([]byte{0xff}), "", false},
		{"empty body allowed", "title", "", true},
		{"body at byte limit", "title", strings.Repeat("a", 256*1024), true},
		{"body above byte limit", "title", strings.Repeat("a", 256*1024+1), false},
		{"multibyte body at byte limit", "title", strings.Repeat("猫", 87381) + "a", true},
		{"multibyte body above byte limit", "title", strings.Repeat("猫", 87382), false},
		{"invalid body UTF-8", "title", string([]byte{0xff}), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.title, tc.markdown)
			if tc.valid && err != nil {
				t.Fatal(err)
			}
			if !tc.valid && !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrImageDisplay) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
		})
	}
}

func TestValidatePublicationRequiresVisibleTitle(t *testing.T) {
	for _, title := range []string{"", " ", "\u3000\n\t"} {
		if !errors.Is(ValidatePublication(title), ErrUntitled) {
			t.Fatalf("%q accepted for publication", title)
		}
	}
	if ValidatePublication(" 題 ") != nil {
		t.Fatal("visible title rejected")
	}
}
