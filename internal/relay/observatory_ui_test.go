package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// --- obsWindow ---

func TestObsWindow(t *testing.T) {
	cases := map[string]int{
		"":      7,
		"1":     1,
		"7":     7,
		"30":    30,
		"14":    7, // unsupported bucket → default
		"abc":   7,
		"-1":    7,
		"99999": 7,
	}
	for raw, want := range cases {
		req := httptest.NewRequest(http.MethodGet, "/observatory/api/v1/burn?window="+raw, nil)
		if got := obsWindow(req); got != want {
			t.Errorf("obsWindow(window=%q) = %d, want %d", raw, got, want)
		}
	}
}

// --- serveObservatoryStatic ---

func newObsStaticRelay(fs fstest.MapFS) *Relay {
	return &Relay{ObservatoryStaticFS: fs}
}

func TestServeObservatoryStaticServesAsset(t *testing.T) {
	r := newObsStaticRelay(fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>obs</title>")},
		"assets/app-abc123.js": {Data: []byte("console.log(1)")},
	})

	req := httptest.NewRequest(http.MethodGet, "/observatory/assets/app-abc123.js", nil)
	w := httptest.NewRecorder()
	r.serveObservatoryStatic(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("asset: expected 200, got %d", w.Code)
	}
	if got := w.Body.String(); got != "console.log(1)" {
		t.Errorf("asset body = %q, want the JS file contents", got)
	}
}

func TestServeObservatoryStaticFallsBackToIndex(t *testing.T) {
	index := "<!doctype html><title>obs</title>"
	r := newObsStaticRelay(fstest.MapFS{
		"index.html": {Data: []byte(index)},
	})

	// react-router client routes have no backing file → must serve index.html.
	for _, path := range []string{
		"/observatory/",
		"/observatory/projects/agt-geonosis",
		"/observatory/agents/cto",
		"/observatory/sessions/abc-123",
		"/observatory/tasks/t1",
		"/observatory/budgets",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.serveObservatoryStatic(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", path, w.Code)
		}
		if w.Body.String() != index {
			t.Errorf("%s: expected SPA index fallback, got %q", path, w.Body.String())
		}
		if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("%s: index Content-Type = %q", path, ct)
		}
	}
}

func TestServeObservatoryStaticNilFS503(t *testing.T) {
	r := &Relay{ObservatoryStaticFS: nil}
	req := httptest.NewRequest(http.MethodGet, "/observatory/", nil)
	w := httptest.NewRecorder()
	r.serveObservatoryStatic(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("nil FS: expected 503, got %d", w.Code)
	}
}

func TestServeObservatoryStaticRejectsNonGet(t *testing.T) {
	r := newObsStaticRelay(fstest.MapFS{"index.html": {Data: []byte("x")}})
	req := httptest.NewRequest(http.MethodPost, "/observatory/", nil)
	w := httptest.NewRecorder()
	r.serveObservatoryStatic(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("POST: expected 404, got %d", w.Code)
	}
}

// --- refreshObservatoryViews ---

func TestRefreshObservatoryViewsDisabledIsNoop(t *testing.T) {
	// interval <= 0 disables the loop; it must return immediately without
	// touching the (nil) pool.
	done := make(chan struct{})
	go func() {
		refreshObservatoryViews(context.Background(), nil, 0)
		close(done)
	}()
	select {
	case <-done:
	default:
		// Give the goroutine a beat; if it blocked it would fail the test via
		// the outer timeout. A plain receive is enough since it returns at once.
		<-done
	}
}
