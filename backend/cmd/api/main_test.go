package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthCheckRejectsFailureAndRedirect(t *testing.T) {
	for _, status := range []int{200, 302, 503} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if status == 302 {
				w.Header().Set("Location", "/login")
			}
			w.WriteHeader(status)
		}))
		err := checkHealth(server.URL)
		server.Close()
		if (err == nil) != (status == 200) {
			t.Fatalf("status %d: error %v", status, err)
		}
	}
}
