package main

import "testing"

func TestUntrustedMetadataIsRejectedBeforeDatabaseAccess(t *testing.T) {
	valid := `[{"id":"42","title":"example","url":"https://www.wantedly.com/companies/example/post_articles/42","publishedAt":"2024-01-01T00:00:00Z"}]`
	if posts, err := parseItems(valid); err != nil || len(posts) != 1 {
		t.Fatalf("valid metadata: %v", err)
	}
	for _, raw := range []string{valid + `{}`, `[]`, `[{"body":"unwanted article body"}]`, `[{"id":"42","title":"example","url":"https://www.wantedly.com/companies/example/post_articles/43","publishedAt":"2024-01-01T00:00:00Z"}]`} {
		if _, err := parseItems(raw); err == nil {
			t.Fatal("untrusted metadata accepted")
		}
	}
}
