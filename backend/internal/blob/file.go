// Package blob provides private object storage for both images and backups.
package blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var ErrNotFound = errors.New("object not found")

var validKey = regexp.MustCompile(`^(images|backups)/[A-Za-z0-9_-]{43}\.(png|jpg|dump|json|json\.gz)$`)

type Files struct{ Directory string }

func (f Files) path(key string) (string, error) {
	if !validKey.MatchString(key) {
		return "", errors.New("invalid object key")
	}
	return filepath.Join(f.Directory, filepath.FromSlash(key)), nil
}
func (f Files) Put(ctx context.Context, key string, body []byte, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := f.path(key)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := io.Copy(file, bytes.NewReader(body))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		os.Remove(path)
		return errors.New("object write failed")
	}
	return nil
}
func (f Files) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := f.path(key)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return file, err
}
func (f Files) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := f.path(key)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
