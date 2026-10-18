package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"testing"
	"time"

	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/blob"
	"coursek/blog/backend/internal/blog"
	"coursek/blog/backend/internal/images"
)

type registrationResponseLost struct {
	images.Repository
	saved images.Image
}

func (r *registrationResponseLost) SaveImage(_ context.Context, asset images.Image) error {
	r.saved = asset
	return errors.New("fixture: response lost after database commit")
}
func (r *registrationResponseLost) GetImage(_ context.Context, id string) (images.Image, error) {
	if id != r.saved.ID {
		return images.Image{}, blog.ErrNotFound
	}
	return r.saved, nil
}
func (r *registrationResponseLost) ImagePublic(context.Context, string) (bool, error) {
	return false, nil
}

func TestUnknownImageRegistrationKeepsAssetPrivateAndAvailableForCommittedMetadata(t *testing.T) {
	store := fake()
	token := "test-only-session"
	store.sessions[auth.Digest(token)] = time.Now().Add(time.Hour)
	hash, err := auth.HashPassword("fixture-password-only")
	if err != nil {
		t.Fatal(err)
	}
	repository := &registrationResponseLost{}
	storage := blob.Files{Directory: t.TempDir()}
	handler, err := NewWithFeatures(store, "https://blog.example", hash, Features{Images: &images.Service{Repository: repository, Blobs: storage}})
	if err != nil {
		t.Fatal(err)
	}
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string][]byte{"data": picture.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: sessionCookie, Value: token}
	response := request(handler, "POST", "/api/admin/images", string(payload), "https://blog.example", cookie)
	if response.Code != 500 || !images.ValidID(repository.saved.ID) {
		t.Fatal("unknown registration contract", response.Code)
	}
	path := "/api/images/" + repository.saved.ID
	if anonymous := request(handler, "GET", path, "", "", nil); anonymous.Code != 404 {
		t.Fatal("unpublished image leaked")
	}
	if owner := request(handler, "GET", path, "", "", cookie); owner.Code != 200 || owner.Body.Len() == 0 {
		t.Fatal("asset was deleted despite committed metadata", owner.Code)
	}
}
