package publication

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
)

// Only the files Refresh produces may be written, so a bad key cannot leave the directory.
var validKey = regexp.MustCompile(`^(data/manifest\.json|data/articles/(all|[1-9][0-9]{3}-(0[1-9]|1[0-2]))/[1-9][0-9]{0,4}\.json|data/posts/[A-Za-z0-9_-]{1,100}\.json|api/images/[A-Za-z0-9_-]{1,100})$`)

// Directory keeps the public files on local disk for development and tests.
type Directory struct{ Path string }

func (d Directory) file(key string) (string, error) {
	if !validKey.MatchString(key) {
		return "", errors.New("invalid public file key")
	}
	return filepath.Join(d.Path, filepath.FromSlash(key)), nil
}

// Put replaces the file in one step, so a reader never sees a half-written file.
func (d Directory) Put(ctx context.Context, key string, body []byte, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := d.file(key)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".writing-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	_, writeErr := temporary.Write(body)
	closeErr := temporary.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("public file write failed")
	}
	if err = os.Chmod(temporary.Name(), 0644); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

func (d Directory) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := d.file(key)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return body, err
}

func (d Directory) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := d.file(key)
	if err != nil {
		return err
	}
	if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
