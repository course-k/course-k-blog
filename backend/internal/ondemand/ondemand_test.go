package ondemand

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkerRunsEveryTaskEvenWhenOneFails(t *testing.T) {
	ran := []string{}
	task := func(name string, err error) Task {
		return Task{Name: name, Run: func(context.Context) error { ran = append(ran, name); return err }}
	}
	worker := Worker{task("sync", errors.New("source unavailable")), task("backup", nil), task("sweep", nil)}
	err := worker.Run(context.Background())
	if strings.Join(ran, ",") != "sync,backup,sweep" {
		t.Fatalf("a failed task must not stop the others: %v", ran)
	}
	if err == nil || !strings.Contains(err.Error(), "sync") || strings.Contains(err.Error(), "source unavailable") {
		t.Fatalf("the error must name the failed task without its details: %v", err)
	}
	if err := (Worker{task("backup", nil)}).Run(context.Background()); err != nil {
		t.Fatalf("a clean run must succeed: %v", err)
	}
}

func TestWorkEndpointReportsFailureSoTheInvocationIsRetried(t *testing.T) {
	failing := Worker{{Name: "backup", Run: func(context.Context) error { return errors.New("storage unavailable") }}}
	response := httptest.NewRecorder()
	failing.ServeHTTP(response, httptest.NewRequest("POST", WorkPath, strings.NewReader(`{}`)))
	if response.Code != 500 || strings.Contains(response.Body.String(), "storage unavailable") {
		t.Fatalf("failed work must answer 500 without details: %d %q", response.Code, response.Body.String())
	}
	clean := httptest.NewRecorder()
	Worker{}.ServeHTTP(clean, httptest.NewRequest("POST", WorkPath, strings.NewReader(`{}`)))
	if clean.Code != 200 {
		t.Fatalf("finished work must answer 200: %d", clean.Code)
	}
	wrongMethod := httptest.NewRecorder()
	Worker{}.ServeHTTP(wrongMethod, httptest.NewRequest("GET", WorkPath, nil))
	if wrongMethod.Code != 405 {
		t.Fatalf("only POST starts work: %d", wrongMethod.Code)
	}
}

// api answers with the given status for every request.
func api(status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
}

func TestWorkIsRequestedAfterASuccessfulOwnerChangeOnly(t *testing.T) {
	cases := []struct {
		method, path string
		status       int
		want         int
	}{
		{"POST", "/api/admin/posts/a/publish", 200, 1},
		{"PUT", "/api/admin/posts/a", 200, 1},
		{"POST", "/api/admin/sync/qiita", 202, 1},
		{"POST", "/api/admin/posts", 201, 1},
		{"GET", "/api/admin/posts", 200, 0},
		{"POST", "/api/admin/posts/a/publish", 409, 0},
		{"POST", "/api/admin/posts/a/publish", 401, 0},
		{"POST", "/api/session", 200, 0},
		{"GET", "/api/articles", 200, 0},
	}
	for _, c := range cases {
		requested := 0
		handler := AfterChange(api(c.status), func(context.Context) { requested++ })
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(c.method, c.path, nil))
		if requested != c.want {
			t.Errorf("%s %s -> %d: work requested %d times, want %d", c.method, c.path, c.status, requested, c.want)
		}
	}
}

func TestAfterChangeTreatsAnUnsetStatusAsSuccess(t *testing.T) {
	requested := 0
	silent := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	AfterChange(silent, func(context.Context) { requested++ }).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/admin/backups", nil))
	if requested != 1 {
		t.Fatalf("a body written without an explicit status is a 200: %d", requested)
	}
}

func TestRouteSeparatesWorkFromTheAPI(t *testing.T) {
	worked := 0
	worker := Worker{{Name: "sweep", Run: func(context.Context) error { worked++; return nil }}}
	handler := Route(api(204), worker)
	work := httptest.NewRecorder()
	handler.ServeHTTP(work, httptest.NewRequest("POST", WorkPath, strings.NewReader(`{}`)))
	other := httptest.NewRecorder()
	handler.ServeHTTP(other, httptest.NewRequest("GET", "/api/healthz", nil))
	if work.Code != 200 || worked != 1 || other.Code != 204 {
		t.Fatalf("work %d (ran %d), api %d", work.Code, worked, other.Code)
	}
}
