package main

import (
	"encoding/json"
	"testing"

	"coursek/blog/backend/internal/backup"
)

func TestRejectsLiveOrUnprotectedTargets(t *testing.T) {
	valid := []string{"mysql://user:password@restore.invalid:3306/blog_restore?tls=verify-full", "live.invalid", "restore.invalid", "course-k-blog-production-restoremediabucket-test", "course-k-blog-production-backupsbucket-test"}
	if !safeTarget(valid[0], valid[1], valid[2], valid[3], valid[4]) {
		t.Fatal("isolated target rejected")
	}
	for name, change := range map[string]func([]string){
		"live host":        func(v []string) { v[1] = v[2] },
		"missing source":   func(v []string) { v[1] = "" },
		"different target": func(v []string) { v[2] = "other.invalid" },
		"live database":    func(v []string) { v[0] = "mysql://user@restore.invalid/blog?tls=verify-full" },
		"plaintext":        func(v []string) { v[0] = "mysql://user@restore.invalid/blog_restore?tls=false" },
		"live media":       func(v []string) { v[3] = "course-k-blog-production-imagesbucket-live" },
		"backup target":    func(v []string) { v[3] = v[4] },
	} {
		t.Run(name, func(t *testing.T) {
			v := append([]string(nil), valid...)
			change(v)
			if safeTarget(v[0], v[1], v[2], v[3], v[4]) {
				t.Fatal("unsafe target accepted")
			}
		})
	}
}

func TestComparisonChecksDataWithoutDependingOnSQLOrder(t *testing.T) {
	expected := backup.Snapshot{DatabaseEngine: "mysql", SchemaVersion: 6, Generation: 1, Tables: map[string]json.RawMessage{
		"posts": json.RawMessage(`[{"id":"b","published":0,"body":"draft"},{"id":"a","published":1,"body":"public"}]`),
	}}
	actual := backup.Snapshot{DatabaseEngine: "mysql", SchemaVersion: 6, Generation: 2, Tables: map[string]json.RawMessage{
		"posts": json.RawMessage(`[{"body":"public","published":1,"id":"a"},{"body":"draft","id":"b","published":0}]`),
	}}
	if !equalSnapshots(expected, actual) {
		t.Fatal("row order, property order and dirty generation must not change equivalence")
	}
	for _, data := range []string{
		`[{"id":"b","published":1,"body":"draft"},{"id":"a","published":1,"body":"public"}]`,
		`[{"id":"a","published":1,"body":"public"}]`,
		`[{"id":"b","published":0,"body":"changed"},{"id":"a","published":1,"body":"public"}]`,
		`invalid`,
	} {
		actual.Tables["posts"] = json.RawMessage(data)
		if equalSnapshots(expected, actual) {
			t.Fatal("changed or invalid content accepted")
		}
	}
	expected.Tables["posts"] = json.RawMessage(`[{"n":9007199254740992}]`)
	actual.Tables["posts"] = json.RawMessage(`[{"n":9007199254740993}]`)
	if equalSnapshots(expected, actual) {
		t.Fatal("large integer precision lost")
	}
}
