package relay

// Observatory integration tests — require a live Postgres instance.
//
// Set WRAITH_OBSERVATORY_TEST_DB_URL to a postgres:// DSN to run.
// Tests are skipped when the variable is absent so the standard
// SQLite-only suite (go test ./...) continues to work without Postgres.
//
// GHA runs these via a postgres service container; see .github/workflows/test.yml.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-relay/internal/config"
	"agent-relay/internal/db"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/server"
)

const obsTestDBURLEnv = "WRAITH_OBSERVATORY_TEST_DB_URL"

// obsIntegRelay opens a live Postgres pool and returns a test Relay.
// Skips the calling test when WRAITH_OBSERVATORY_TEST_DB_URL is not set.
func obsIntegRelay(t *testing.T) (*Relay, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv(obsTestDBURLEnv)
	if dsn == "" {
		t.Skipf("skipping observatory integration tests — set %s to run", obsTestDBURLEnv)
	}

	ctx := context.Background()
	pool, err := db.NewPGPool(ctx, dsn, nil)
	if err != nil || pool == nil {
		t.Fatalf("connect observatory db: %v (pool=%v)", err, pool)
	}
	t.Cleanup(func() { pool.Close() })

	if err := db.RunObservatoryMigrations(ctx, pool); err != nil {
		t.Fatalf("run observatory migrations: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "relay_obs_integ.db")
	relayDB, err := db.NewTestDB(dbPath)
	if err != nil {
		t.Fatalf("NewTestDB: %v", err)
	}
	t.Cleanup(func() { _ = relayDB.Close() })

	mcpSrv := server.NewMCPServer("test", "0.0.0")
	reg := NewSessionRegistry(mcpSrv)
	evBus := NewEventBus()
	h := NewHandlers(relayDB, reg, nil, nil, evBus)

	r := &Relay{
		DB:       relayDB,
		PGPool:   pool,
		Handlers: h,
		Config: config.Config{
			ObservatoryEnabled: true,
			DevMode:            false,
		},
	}
	return r, pool
}

// obsPostIngest fires a POST to the ingest mux and returns the recorder.
func obsPostIngest(t *testing.T, r *Relay, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeObservatoryIngest(w, req)
	return w
}

// obsGetRead fires a GET to the read mux (behind EasyAuth middleware).
func obsGetRead(t *testing.T, r *Relay, path, principalHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if principalHeader != "" {
		req.Header.Set("X-MS-CLIENT-PRINCIPAL", principalHeader)
	}
	w := httptest.NewRecorder()
	r.ObservatoryEasyAuthMiddleware(http.HandlerFunc(r.ServeObservatoryRead)).ServeHTTP(w, req)
	return w
}

// obsDecodeJSON unmarshals a recorder body into v.
func obsDecodeJSON(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatalf("decode JSON response (status=%d): %v\nbody: %s", w.Code, err, w.Body.String())
	}
}

// obsAssert202 asserts the recorder has HTTP 202.
func obsAssert202(t *testing.T, w *httptest.ResponseRecorder, context string) {
	t.Helper()
	if w.Code != http.StatusAccepted {
		t.Fatalf("%s: expected 202, got %d — body: %s", context, w.Code, w.Body.String())
	}
}

// ─── synthetic end-to-end ────────────────────────────────────────────────────

// TestObservatoryIntegration_E2E is the primary end-to-end test covering the
// full ingest sequence and read-back assertions described in T8 scope.
func TestObservatoryIntegration_E2E(t *testing.T) {
	r, pool := obsIntegRelay(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Millisecond)
	workerRunID := uuid.New().String()
	sessionID := uuid.New().String()
	agentID := fmt.Sprintf("test-worker-%s", uuid.New().String()[:8])
	profileSlug := "agt-kuat-backend"

	// ── 1. Register worker_run ──────────────────────────────────────────────
	w := obsPostIngest(t, r, "/observatory/api/v1/ingest/worker_runs", map[string]any{
		"worker_run_id": workerRunID,
		"agent_id":      agentID,
		"started_at":    now.Format(time.RFC3339),
		"mode":          "pool",
	})
	obsAssert202(t, w, "register worker_run")
	var wrResp map[string]string
	obsDecodeJSON(t, w, &wrResp)
	if wrResp["status"] != "registered" {
		t.Errorf("worker_run: want status=registered, got %q", wrResp["status"])
	}

	// Assert row landed in worker_runs.
	var workerRunExists bool
	if err := pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM worker_runs WHERE worker_run_id = $1::uuid)",
		workerRunID,
	).Scan(&workerRunExists); err != nil {
		t.Fatalf("check worker_runs: %v", err)
	}
	if !workerRunExists {
		t.Error("worker_runs: expected row, got none")
	}

	// ── 2. Register session ─────────────────────────────────────────────────
	w = obsPostIngest(t, r, "/observatory/api/v1/ingest/sessions", map[string]any{
		"session_id":    sessionID,
		"worker_run_id": workerRunID,
		"spawn_index":   0,
		"model":         "claude-sonnet-4-6",
		"started_at":    now.Add(time.Second).Format(time.RFC3339),
		"profile_slug":  profileSlug,
	})
	obsAssert202(t, w, "register session")
	var sessResp map[string]string
	obsDecodeJSON(t, w, &sessResp)
	if sessResp["status"] != "registered" {
		t.Errorf("session: want status=registered, got %q", sessResp["status"])
	}

	// Assert row in sessions with agent_id linked to the persona.
	var sessionAgentID string
	if err := pool.QueryRow(ctx,
		"SELECT COALESCE(agent_id, '') FROM sessions WHERE session_id = $1::uuid",
		sessionID,
	).Scan(&sessionAgentID); err != nil {
		t.Fatalf("check sessions: %v", err)
	}
	if sessionAgentID != profileSlug {
		t.Errorf("sessions.agent_id: want %q, got %q", profileSlug, sessionAgentID)
	}

	// ── 3. POST events batch with planted AWS access key ────────────────────
	// AKIAIOSFODNN7EXAMPLE matches the secret_aws_access_key_id rule.
	plantedKey := "AKIAIOSFODNN7EXAMPLE"
	w = obsPostIngest(t, r, "/observatory/api/v1/ingest/events", map[string]any{
		"events": []any{map[string]any{
			"session_id": sessionID,
			"ts":         now.Add(2 * time.Second).Format(time.RFC3339Nano),
			"kind":       "tool_use",
			"tool":       "Bash",
			"input":      map[string]any{"command": "echo " + plantedKey},
			"output":     map[string]any{"result": "ok"},
		}},
		"token_deltas": []any{map[string]any{
			"session_id":                     sessionID,
			"model":                          "claude-sonnet-4-6",
			"ts":                             now.Add(2 * time.Second).Format(time.RFC3339Nano),
			"input_tokens":                   1000,
			"output_tokens":                  200,
			"cache_read_input_tokens":        0,
			"cache_creation_input_tokens_5m": 0,
			"cache_creation_input_tokens_1h": 0,
		}},
	})
	obsAssert202(t, w, "ingest events batch")

	var evResp map[string]any
	obsDecodeJSON(t, w, &evResp)
	if got := int(evResp["accepted_events"].(float64)); got != 1 {
		t.Errorf("events batch: want accepted_events=1, got %d", got)
	}
	if got := int(evResp["accepted_token_deltas"].(float64)); got != 1 {
		t.Errorf("events batch: want accepted_token_deltas=1, got %d", got)
	}
	if got := int(evResp["flags_fired"].(float64)); got != 1 {
		t.Errorf("events batch: want flags_fired=1, got %d", got)
	}

	// Assert events row: input JSONB must contain redacted placeholder.
	var inputJSON string
	if err := pool.QueryRow(ctx,
		"SELECT input FROM events WHERE session_id = $1::uuid ORDER BY event_id DESC LIMIT 1",
		sessionID,
	).Scan(&inputJSON); err != nil {
		t.Fatalf("query events: %v", err)
	}
	if strings.Contains(inputJSON, plantedKey) {
		t.Errorf("events.input must not contain the raw key %q — got: %s", plantedKey, inputJSON)
	}
	redactedTag := "[REDACTED:secret_aws_access_key_id]"
	if !strings.Contains(inputJSON, redactedTag) {
		t.Errorf("events.input must contain %q — got: %s", redactedTag, inputJSON)
	}

	// Assert flags row with correct rule_id.
	var flagRuleID, flagSeverity string
	if err := pool.QueryRow(ctx,
		"SELECT rule_id, severity FROM flags WHERE session_id = $1::uuid ORDER BY flag_id DESC LIMIT 1",
		sessionID,
	).Scan(&flagRuleID, &flagSeverity); err != nil {
		t.Fatalf("query flags: %v", err)
	}
	if flagRuleID != "secret_aws_access_key_id" {
		t.Errorf("flags.rule_id: want secret_aws_access_key_id, got %q", flagRuleID)
	}
	if flagSeverity != "high" {
		t.Errorf("flags.severity: want high, got %q", flagSeverity)
	}

	// Assert token_deltas row landed.
	var deltaCount int
	if err := pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM token_deltas WHERE session_id = $1::uuid",
		sessionID,
	).Scan(&deltaCount); err != nil {
		t.Fatalf("query token_deltas: %v", err)
	}
	if deltaCount != 1 {
		t.Errorf("token_deltas: want 1 row, got %d", deltaCount)
	}

	// ── 4. Finalize session ─────────────────────────────────────────────────
	w = obsPostIngest(t, r, "/observatory/api/v1/ingest/sessions/finalize", map[string]any{
		"session_id":  sessionID,
		"ended_at":    now.Add(60 * time.Second).Format(time.RFC3339),
		"duration_ms": 60000,
		"turns":       3,
	})
	obsAssert202(t, w, "finalize session")
	var finalResp map[string]string
	obsDecodeJSON(t, w, &finalResp)
	if finalResp["status"] != "finalized" {
		t.Errorf("finalize: want status=finalized, got %q", finalResp["status"])
	}

	// Assert ended_at and turns updated.
	var endedAt *time.Time
	var turns *int
	if err := pool.QueryRow(ctx,
		"SELECT ended_at, turns FROM sessions WHERE session_id = $1::uuid",
		sessionID,
	).Scan(&endedAt, &turns); err != nil {
		t.Fatalf("query session post-finalize: %v", err)
	}
	if endedAt == nil {
		t.Error("sessions.ended_at: expected non-NULL after finalize")
	}
	if turns == nil || *turns != 3 {
		t.Errorf("sessions.turns: expected 3, got %v", turns)
	}
}

// ─── estimates upsert ────────────────────────────────────────────────────────

// TestObservatoryIntegration_EstimateUpsert verifies that posting the same
// (task_id, estimator_source) twice upserts rather than inserting a duplicate.
func TestObservatoryIntegration_EstimateUpsert(t *testing.T) {
	r, pool := obsIntegRelay(t)
	ctx := context.Background()

	taskID := uuid.New().String()
	now := time.Now().UTC().Truncate(time.Millisecond)

	first := map[string]any{
		"task_id":           taskID,
		"estimator_source":  "executor_first_turn",
		"estimated_at":      now.Format(time.RFC3339),
		"complexity":        "M",
		"est_tokens_input":  10000,
		"est_tokens_output": 2000,
		"est_duration_s":    120,
		"est_files":         3,
		"est_risk_flags":    []string{"schema_change"},
	}
	second := map[string]any{
		"task_id":           taskID,
		"estimator_source":  "executor_first_turn",
		"estimated_at":      now.Add(time.Second).Format(time.RFC3339),
		"complexity":        "L",
		"est_tokens_input":  20000,
		"est_tokens_output": 4000,
		"est_duration_s":    240,
		"est_files":         5,
		"est_risk_flags":    []string{"schema_change", "external_api"},
	}

	w := obsPostIngest(t, r, "/observatory/api/v1/ingest/estimates", first)
	obsAssert202(t, w, "estimate first post")

	w = obsPostIngest(t, r, "/observatory/api/v1/ingest/estimates", second)
	obsAssert202(t, w, "estimate second post (upsert)")

	// Exactly one row — composite PK prevents duplicates.
	var rowCount int
	if err := pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM task_estimates WHERE task_id = $1 AND estimator_source = 'executor_first_turn'",
		taskID,
	).Scan(&rowCount); err != nil {
		t.Fatalf("query task_estimates: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("estimate upsert: want 1 row, got %d", rowCount)
	}

	// Second post updated the fields.
	var complexity string
	var estFiles int
	if err := pool.QueryRow(ctx,
		"SELECT complexity, est_files FROM task_estimates WHERE task_id = $1 AND estimator_source = 'executor_first_turn'",
		taskID,
	).Scan(&complexity, &estFiles); err != nil {
		t.Fatalf("query task_estimates fields: %v", err)
	}
	if complexity != "L" {
		t.Errorf("estimate upsert: complexity want L, got %q", complexity)
	}
	if estFiles != 5 {
		t.Errorf("estimate upsert: est_files want 5, got %d", estFiles)
	}
}

// ─── task_run FK miss ────────────────────────────────────────────────────────

// TestObservatoryIntegration_TaskRunFKMiss verifies that a task_run referencing
// a non-existent session returns {status: "dropped"} rather than 5xx.
func TestObservatoryIntegration_TaskRunFKMiss(t *testing.T) {
	r, _ := obsIntegRelay(t)

	w := obsPostIngest(t, r, "/observatory/api/v1/ingest/task_runs", map[string]any{
		"task_run_id": uuid.New().String(),
		"task_id":     uuid.New().String(),
		"session_id":  uuid.New().String(), // does not exist in sessions
	})
	obsAssert202(t, w, "task_run FK miss")

	var resp map[string]string
	obsDecodeJSON(t, w, &resp)
	if resp["status"] != "dropped" {
		t.Errorf("task_run FK miss: want status=dropped, got %q", resp["status"])
	}
}

// ─── read endpoints ──────────────────────────────────────────────────────────

// obsSetupWorkerAndSession inserts a worker_run + session and returns their IDs.
func obsSetupWorkerAndSession(t *testing.T, r *Relay) (workerRunID, sessionID string) {
	t.Helper()
	workerRunID = uuid.New().String()
	sessionID = uuid.New().String()
	now := time.Now().UTC()

	w := obsPostIngest(t, r, "/observatory/api/v1/ingest/worker_runs", map[string]any{
		"worker_run_id": workerRunID,
		"agent_id":      "test-worker-" + workerRunID[:8],
		"started_at":    now.Format(time.RFC3339),
		"mode":          "pool",
	})
	obsAssert202(t, w, "setup worker_run")

	w = obsPostIngest(t, r, "/observatory/api/v1/ingest/sessions", map[string]any{
		"session_id":    sessionID,
		"worker_run_id": workerRunID,
		"spawn_index":   0,
		"model":         "claude-haiku-4-5",
		"started_at":    now.Add(time.Second).Format(time.RFC3339),
	})
	obsAssert202(t, w, "setup session")
	return
}

// TestObservatoryIntegration_ReadEventsPaginated seeds 3 events then reads
// them back in two pages (limit=2, then after_id cursor).
func TestObservatoryIntegration_ReadEventsPaginated(t *testing.T) {
	r, _ := obsIntegRelay(t)
	_, sessionID := obsSetupWorkerAndSession(t, r)
	now := time.Now().UTC()

	// Seed 3 tool_use events (no secrets — plain input).
	for i := 0; i < 3; i++ {
		w := obsPostIngest(t, r, "/observatory/api/v1/ingest/events", map[string]any{
			"events": []any{map[string]any{
				"session_id": sessionID,
				"ts":         now.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano),
				"kind":       "tool_use",
				"tool":       "Read",
				"input":      map[string]any{"file": fmt.Sprintf("/tmp/file%d.txt", i)},
			}},
			"token_deltas": []any{},
		})
		obsAssert202(t, w, fmt.Sprintf("seed event %d", i))
	}

	auth := easyAuthHeader("reviewer@example.com")

	// Page 1: limit=2
	page1 := obsGetRead(t, r, "/observatory/api/v1/sessions/"+sessionID+"/events?limit=2", auth)
	if page1.Code != http.StatusOK {
		t.Fatalf("GET events page 1: want 200, got %d — %s", page1.Code, page1.Body.String())
	}
	var events1 []obsEvent
	obsDecodeJSON(t, page1, &events1)
	if len(events1) != 2 {
		t.Fatalf("page 1: want 2 events, got %d", len(events1))
	}

	// Page 2: after_id=last event_id from page 1
	cursor := events1[len(events1)-1].EventID
	page2 := obsGetRead(t, r,
		fmt.Sprintf("/observatory/api/v1/sessions/%s/events?limit=2&after_id=%d", sessionID, cursor),
		auth,
	)
	if page2.Code != http.StatusOK {
		t.Fatalf("GET events page 2: want 200, got %d — %s", page2.Code, page2.Body.String())
	}
	var events2 []obsEvent
	obsDecodeJSON(t, page2, &events2)
	if len(events2) != 1 {
		t.Fatalf("page 2: want 1 event, got %d", len(events2))
	}
	if events2[0].EventID <= cursor {
		t.Errorf("page 2 event_id (%d) must be > cursor (%d)", events2[0].EventID, cursor)
	}
}

// TestObservatoryIntegration_EstimateWinnerViaView verifies that v_task_estimate
// picks the dispatcher_envelope row over executor_first_turn.
func TestObservatoryIntegration_EstimateWinnerViaView(t *testing.T) {
	r, _ := obsIntegRelay(t)
	taskID := uuid.New().String()
	now := time.Now().UTC()
	auth := easyAuthHeader("analyst@example.com")

	// Lower-priority estimate first.
	w := obsPostIngest(t, r, "/observatory/api/v1/ingest/estimates", map[string]any{
		"task_id":           taskID,
		"estimator_source":  "executor_first_turn",
		"estimated_at":      now.Format(time.RFC3339),
		"complexity":        "S",
		"est_tokens_input":  5000,
		"est_tokens_output": 1000,
		"est_duration_s":    60,
		"est_files":         2,
		"est_risk_flags":    []string{},
	})
	obsAssert202(t, w, "estimate executor_first_turn")

	// Higher-priority estimate.
	w = obsPostIngest(t, r, "/observatory/api/v1/ingest/estimates", map[string]any{
		"task_id":           taskID,
		"estimator_source":  "dispatcher_envelope",
		"estimated_at":      now.Format(time.RFC3339),
		"complexity":        "L",
		"est_tokens_input":  50000,
		"est_tokens_output": 10000,
		"est_duration_s":    600,
		"est_files":         8,
		"est_risk_flags":    []string{"external_api"},
	})
	obsAssert202(t, w, "estimate dispatcher_envelope")

	// GET /tasks/{id}/estimate — must return dispatcher_envelope.
	rr := obsGetRead(t, r, "/observatory/api/v1/tasks/"+taskID+"/estimate", auth)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET estimate: want 200, got %d — %s", rr.Code, rr.Body.String())
	}
	var est obsTaskEstimate
	obsDecodeJSON(t, rr, &est)
	if est.EstimatorSource != "dispatcher_envelope" {
		t.Errorf("v_task_estimate winner: want dispatcher_envelope, got %q", est.EstimatorSource)
	}
	if est.Complexity != "L" {
		t.Errorf("v_task_estimate complexity: want L, got %q", est.Complexity)
	}
}

// TestObservatoryIntegration_SessionNotFound verifies that GET /sessions/{unknown}
// returns HTTP 404.
func TestObservatoryIntegration_SessionNotFound(t *testing.T) {
	r, _ := obsIntegRelay(t)
	auth := easyAuthHeader("reader@example.com")

	rr := obsGetRead(t, r, "/observatory/api/v1/sessions/"+uuid.New().String(), auth)
	if rr.Code != http.StatusNotFound {
		t.Errorf("GET unknown session: want 404, got %d — %s", rr.Code, rr.Body.String())
	}
}

// ─── EasyAuth gate ───────────────────────────────────────────────────────────

// TestObservatoryIntegration_EasyAuthGatesRead confirms that ingest routes
// accept requests with no auth header, while read routes enforce auth.
func TestObservatoryIntegration_EasyAuthGatesRead(t *testing.T) {
	r, _ := obsIntegRelay(t)
	_, sessionID := obsSetupWorkerAndSession(t, r)
	path := "/observatory/api/v1/sessions/" + sessionID

	// Ingest has no auth requirement — worker_runs POST works without header.
	// (Covered by the full E2E test above; here we just confirm the read split.)

	// No auth header → 401.
	rr := obsGetRead(t, r, path, "")
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("read without auth: want 401, got %d", rr.Code)
	}

	// Valid header → 200.
	rr = obsGetRead(t, r, path, easyAuthHeader("operator@example.com"))
	if rr.Code != http.StatusOK {
		t.Errorf("read with auth: want 200, got %d — %s", rr.Code, rr.Body.String())
	}
}
