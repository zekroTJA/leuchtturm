package docker

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/require"
)

var apiVersionPrefixPattern = regexp.MustCompile(`^/v[0-9.]+`)

// fakeRequest is a request recorded by the fakeDaemon.
type fakeRequest struct {
	Method string
	Path   string
	Query  map[string][]string
}

// fakeDaemon is a minimal fake of the Docker Engine API used to test the Controller without a
// real Docker daemon. Handlers are registered by "METHOD /path" with the API version prefix
// stripped. Requests without a registered handler are answered with 404.
type fakeDaemon struct {
	Server *httptest.Server

	mtx      sync.Mutex
	handlers map[string]http.HandlerFunc
	requests []fakeRequest
}

func newFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()

	daemon := &fakeDaemon{
		handlers: make(map[string]http.HandlerFunc),
	}
	daemon.Server = httptest.NewServer(http.HandlerFunc(daemon.serveHTTP))
	t.Cleanup(daemon.Server.Close)

	return daemon
}

func (t *fakeDaemon) serveHTTP(w http.ResponseWriter, r *http.Request) {
	path := apiVersionPrefixPattern.ReplaceAllString(r.URL.Path, "")
	key := r.Method + " " + path

	t.mtx.Lock()
	t.requests = append(t.requests, fakeRequest{
		Method: r.Method,
		Path:   path,
		Query:  r.URL.Query(),
	})
	handler, ok := t.handlers[key]
	t.mtx.Unlock()

	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "no such route: " + key})
		return
	}
	handler(w, r)
}

func (t *fakeDaemon) handle(method string, path string, handler http.HandlerFunc) {
	t.mtx.Lock()
	defer t.mtx.Unlock()
	t.handlers[method+" "+path] = handler
}

func (t *fakeDaemon) handleJSON(method string, path string, status int, body any) {
	t.handle(method, path, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, status, body)
	})
}

func (t *fakeDaemon) requestKeys() []string {
	t.mtx.Lock()
	defer t.mtx.Unlock()

	keys := make([]string, 0, len(t.requests))
	for _, request := range t.requests {
		keys = append(keys, request.Method+" "+request.Path)
	}
	return keys
}

func (t *fakeDaemon) findRequest(method string, path string) (request fakeRequest, found bool) {
	t.mtx.Lock()
	defer t.mtx.Unlock()

	for _, request := range t.requests {
		if request.Method == method && request.Path == path {
			return request, true
		}
	}
	return fakeRequest{}, false
}

func (t *fakeDaemon) newClient(tb testing.TB) *client.Client {
	tb.Helper()

	cl, err := client.New(
		client.WithHost("tcp://"+strings.TrimPrefix(t.Server.URL, "http://")),
		client.WithAPIVersion(client.MaxAPIVersion),
	)
	require.NoError(tb, err)
	tb.Cleanup(func() { _ = cl.Close() })

	return cl
}

func (t *fakeDaemon) newController(tb testing.TB, defaultSchedule string) *Controller {
	tb.Helper()

	return &Controller{
		client:              t.newClient(tb),
		scheduler:           cron.New(),
		scheduledJobs:       make(map[string]cron.EntryID),
		eventReconnectDelay: 10 * time.Millisecond,
		defaultSchedule:     defaultSchedule,
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}
