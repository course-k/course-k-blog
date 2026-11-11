package main

import (
	"strings"
	"testing"

	"coursek/blog/backend/internal/ondemand"
	"coursek/blog/backend/internal/publication"
)

func names(worker ondemand.Worker) string {
	out := []string{}
	for _, task := range worker {
		out = append(out, task.Name)
	}
	return strings.Join(out, ",")
}

func TestPendingWorkFollowsTheEnabledFeatures(t *testing.T) {
	if got := names(pendingWork(true, true, nil, nil, nil, &publication.Publisher{})); got != "external_sync,publication,backup,upload_sweep" {
		t.Fatalf("every enabled timer must have its task, with reader files refreshed before the backup: %s", got)
	}
	if got := names(pendingWork(false, false, nil, nil, nil, nil)); got != "upload_sweep" {
		t.Fatalf("disabled features must not run: %s", got)
	}
}
