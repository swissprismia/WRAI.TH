package relay

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"agent-relay/internal/config"
	"agent-relay/internal/db"

	"github.com/mark3labs/mcp-go/server"
)

// testObservatoryRelay builds a minimal Relay for observatory handler tests.
// Pass obsDB=nil to simulate a missing pool.
func testObservatoryRelay(t *testing.T, enabled, devMode bool) *Relay {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "relay_obs_test.db")
	database, err := db.NewTestDB(dbPath)
	if err != nil {
		t.Fatalf("NewTestDB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	mcpSrv := server.NewMCPServer("test", "0.0.0")
	registry := NewSessionRegistry(mcpSrv)
	events := NewEventBus()
	handlers := NewHandlers(database, registry, nil, nil, events)

	return &Relay{
		DB:            database,
		PGPool: nil, // nil by default; tests that need a pool set it themselves
		Handlers:      handlers,
		Config: config.Config{
			ObservatoryEnabled: enabled,
			DevMode:            devMode,
		},
	}
}

// --- ServeObservatoryIngest tests ---

func TestObservatoryIngestNilPool503(t *testing.T) {
	r := testObservatoryRelay(t, true, false)
	// r.PGPool is nil

	for _, path := range []string{
		"/observatory/api/v1/ingest/worker_runs",
		"/observatory/api/v1/ingest/sessions",
		"/observatory/api/v1/ingest/sessions/finalize",
		"/observatory/api/v1/ingest/events",
		"/observatory/api/v1/ingest/estimates",
		"/observatory/api/v1/ingest/task_runs",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		w := httptest.NewRecorder()
		r.ServeObservatoryIngest(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("POST %s: expected 503, got %d", path, w.Code)
		}
	}
}

func TestObservatoryIngestNilPoolBeatsRouting(t *testing.T) {
	// nil-pool guard fires before the routing switch, so even unrecognised
	// paths get 503 (not 404) when the pool is absent.
	r := testObservatoryRelay(t, true, false)
	req := httptest.NewRequest(http.MethodGet, "/observatory/api/v1/ingest/unknown", nil)
	w := httptest.NewRecorder()
	r.ServeObservatoryIngest(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for nil pool, got %d", w.Code)
	}
}

// --- ServeObservatoryRead tests ---

func TestObservatoryReadNilPool503(t *testing.T) {
	r := testObservatoryRelay(t, true, false)

	for _, path := range []string{
		"/observatory/api/v1/sessions/abc123/events",
		"/observatory/api/v1/sessions/abc123/token_deltas",
		"/observatory/api/v1/sessions/abc123",
		"/observatory/api/v1/tasks/abc123/estimate",
		"/observatory/api/v1/tasks/abc123/run",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeObservatoryRead(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s: expected 503, got %d", path, w.Code)
		}
	}
}

// --- ObservatoryEasyAuthMiddleware tests ---

func TestObservatoryEasyAuthBlocksMissingHeader(t *testing.T) {
	r := testObservatoryRelay(t, true, false)

	reached := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	handler := r.ObservatoryEasyAuthMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/observatory/api/v1/sessions/x", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without EasyAuth header, got %d", w.Code)
	}
	if reached {
		t.Error("inner handler must not be called when auth fails")
	}
}

func TestObservatoryEasyAuthAllowsValidHeader(t *testing.T) {
	r := testObservatoryRelay(t, true, false)

	reached := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	handler := r.ObservatoryEasyAuthMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/observatory/api/v1/sessions/x", nil)
	req.Header.Set("X-MS-CLIENT-PRINCIPAL", easyAuthHeader("user@example.com"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with valid EasyAuth header, got %d", w.Code)
	}
	if !reached {
		t.Error("inner handler must be called when auth succeeds")
	}
}

func TestObservatoryEasyAuthDevModeFallback(t *testing.T) {
	r := testObservatoryRelay(t, true, true) // DevMode=true

	reached := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	handler := r.ObservatoryEasyAuthMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/observatory/api/v1/sessions/x", nil)
	// No X-MS-CLIENT-PRINCIPAL header — dev mode should pass as dev@local.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected dev-mode fallback to pass, got %d", w.Code)
	}
	if !reached {
		t.Error("inner handler must be reached in dev-mode fallback")
	}
}

// --- path-matching helper tests ---

func TestObsMatchSub(t *testing.T) {
	cases := []struct {
		path, prefix, suffix string
		want                 bool
	}{
		{"/observatory/api/v1/sessions/abc123/events", "/observatory/api/v1/sessions/", "/events", true},
		{"/observatory/api/v1/sessions/a/events", "/observatory/api/v1/sessions/", "/events", true}, // 1-char id
		{"/observatory/api/v1/sessions/abc/token_deltas", "/observatory/api/v1/sessions/", "/token_deltas", true},
		{"/observatory/api/v1/sessions/abc", "/observatory/api/v1/sessions/", "/events", false},
		{"/observatory/api/v1/tasks/xyz/estimate", "/observatory/api/v1/tasks/", "/estimate", true},
		{"/observatory/api/v1/tasks/xyz/run", "/observatory/api/v1/tasks/", "/run", true},
		// empty id (path has // before suffix) — must not match
		{"/observatory/api/v1/sessions//events", "/observatory/api/v1/sessions/", "/events", false},
	}
	for _, tc := range cases {
		got := obsMatchSub(tc.path, tc.prefix, tc.suffix)
		if got != tc.want {
			t.Errorf("obsMatchSub(%q, %q, %q) = %v, want %v", tc.path, tc.prefix, tc.suffix, got, tc.want)
		}
	}
}

func TestObsMatchID(t *testing.T) {
	cases := []struct {
		path, prefix string
		want         bool
	}{
		{"/observatory/api/v1/sessions/abc123", "/observatory/api/v1/sessions/", true},
		{"/observatory/api/v1/sessions/abc123/events", "/observatory/api/v1/sessions/", false},
		{"/observatory/api/v1/sessions/", "/observatory/api/v1/sessions/", false},
		{"/other/path", "/observatory/api/v1/sessions/", false},
	}
	for _, tc := range cases {
		got := obsMatchID(tc.path, tc.prefix)
		if got != tc.want {
			t.Errorf("obsMatchID(%q, %q) = %v, want %v", tc.path, tc.prefix, got, tc.want)
		}
	}
}
