// Package backup creates versioned application snapshots, not database-server dumps.
package backup

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
)

const maxArchiveBytes = 128 * 1024 * 1024

// Bound DB JSON before aggregating it into API-process memory.
const MaxSnapshotBytes = 64 * 1024 * 1024

func encodeJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false) // Archives are not inserted into HTML; avoid expanding article markup.
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

var ErrInvalid = errors.New("backup archive is invalid or unsupported")

type Snapshot struct {
	DatabaseEngine string                     `json:"databaseEngine,omitempty"`
	SchemaVersion  int                        `json:"schemaVersion"`
	Generation     int64                      `json:"generation"`
	Tables         map[string]json.RawMessage `json:"tables"`
}
type envelope struct {
	Format  int             `json:"format"`
	SHA256  string          `json:"sha256"`
	Payload json.RawMessage `json:"payload"`
}

func Encode(snapshot Snapshot) ([]byte, error) {
	payload, err := encodeJSON(snapshot)
	if err != nil {
		return nil, err
	}
	if len(payload) > maxArchiveBytes {
		return nil, ErrInvalid
	}
	hash := sha256.Sum256(payload)
	content, err := encodeJSON(envelope{Format: 1, SHA256: hex.EncodeToString(hash[:]), Payload: payload})
	if err != nil {
		return nil, err
	}
	if len(content) > maxArchiveBytes {
		return nil, ErrInvalid
	}
	var body bytes.Buffer
	writer := gzip.NewWriter(&body)
	if _, err = writer.Write(content); err != nil {
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}
func Decode(body []byte) (Snapshot, error) {
	var snapshot Snapshot
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return snapshot, ErrInvalid
	}
	defer reader.Close()
	content, err := io.ReadAll(io.LimitReader(reader, maxArchiveBytes+1))
	if err != nil || len(content) > maxArchiveBytes {
		return snapshot, ErrInvalid
	}
	var archive envelope
	if json.Unmarshal(content, &archive) != nil || archive.Format != 1 {
		return snapshot, ErrInvalid
	}
	hash := sha256.Sum256(archive.Payload)
	if hex.EncodeToString(hash[:]) != archive.SHA256 {
		return snapshot, ErrInvalid
	}
	if json.Unmarshal(archive.Payload, &snapshot) != nil || snapshot.SchemaVersion < 1 || snapshot.Generation < 0 || snapshot.Tables == nil {
		return snapshot, ErrInvalid
	}
	return snapshot, nil
}
