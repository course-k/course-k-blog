package backup

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestArchiveRoundTripAndIntegrity(t *testing.T) {
	input := Snapshot{SchemaVersion: 4, Generation: 7, Tables: map[string]json.RawMessage{"posts": json.RawMessage(`[{"id":"example"}]`)}}
	body, err := Encode(input)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Decode(body)
	if err != nil || restored.Generation != 7 || !bytes.Equal(restored.Tables["posts"], input.Tables["posts"]) {
		t.Fatal("snapshot roundtrip failed", err)
	}
	body[len(body)/2] ^= 0xff
	if _, err := Decode(body); err == nil {
		t.Fatal("corrupted archive accepted")
	}
	if _, err := Decode(bytes.Repeat([]byte("x"), 100)); err == nil {
		t.Fatal("not an archive accepted")
	}
}
