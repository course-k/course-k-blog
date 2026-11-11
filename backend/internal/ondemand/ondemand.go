// Package ondemand replaces the timers of the long-running server where the
// process only runs while it handles an invocation. Work that used to wait for
// the next tick is requested after each owner change and runs in its own
// invocation.
package ondemand

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// WorkPath receives the invocation that carries out pending work. It is outside
// /api, so the public entry point never routes a browser request to it.
const WorkPath = "/internal/work"

// One invocation may fetch an external source (two minutes) and take a backup.
const workTimeout = 10 * time.Minute

type Task struct {
	Name string
	Run  func(context.Context) error
}

// Worker is the list of tasks the timers used to run, in order.
type Worker []Task

// Run carries out every task. One failure does not stop the others; the
// returned error names the failed tasks without their details.
func (w Worker) Run(ctx context.Context) error {
	failed := []string{}
	for _, task := range w {
		if err := task.Run(ctx); err != nil {
			slog.Error("operation_failed", "operation", "on_demand_work", "code", task.Name)
			failed = append(failed, task.Name)
		}
	}
	if len(failed) > 0 {
		return errors.New("work failed: " + strings.Join(failed, ","))
	}
	return nil
}

// ServeHTTP runs the work before answering: the process is frozen as soon as
// the invocation returns. A failure answers 500 so the invocation is retried.
func (w Worker) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		response.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// The server's write deadline is sized for API requests, not for this work.
	_ = http.NewResponseController(response).SetWriteDeadline(time.Now().Add(workTimeout + time.Minute))
	ctx, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), workTimeout)
	defer cancel()
	if err := w.Run(ctx); err != nil {
		response.WriteHeader(http.StatusInternalServerError)
		return
	}
	response.WriteHeader(http.StatusOK)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}
func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(body)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// AfterChange requests work once an owner change has been saved: a successful
// request to the owner API that is not a read. Reads and refused requests
// change nothing, so they request nothing.
func AfterChange(next http.Handler, request func(context.Context)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		read := r.Method == http.MethodGet || r.Method == http.MethodHead
		saved := recorder.status == 0 || (recorder.status >= 200 && recorder.status < 300)
		if !read && saved && strings.HasPrefix(r.URL.Path, "/api/admin/") {
			request(context.WithoutCancel(r.Context()))
		}
	})
}

// Route sends the work invocation to the worker and everything else to the API.
func Route(api http.Handler, worker Worker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == WorkPath {
			worker.ServeHTTP(w, r)
			return
		}
		api.ServeHTTP(w, r)
	})
}
