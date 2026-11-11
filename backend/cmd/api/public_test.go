package main

import (
	"context"
	"testing"

	"coursek/blog/backend/internal/publication"
)

func TestPublicTargetFollowsConfiguration(t *testing.T) {
	ctx := context.Background()
	if target, err := publicTarget(ctx, "", ""); target != nil || err != nil {
		t.Fatalf("without a destination no files are written: %v %v", target, err)
	}
	if target, err := publicTarget(ctx, "/public", ""); err != nil || target != (publication.Directory{Path: "/public"}) {
		t.Fatalf("a directory must be used as given: %v %v", target, err)
	}
	if target, err := publicTarget(ctx, "", "reader-bucket"); err != nil {
		t.Fatal(err)
	} else if bucket, ok := target.(publication.Bucket); !ok || bucket.Name != "reader-bucket" {
		t.Fatalf("a bucket name must select the bucket: %v", target)
	}
	if _, err := publicTarget(ctx, "/public", "reader-bucket"); err == nil {
		t.Fatal("two destinations must be rejected")
	}
}
