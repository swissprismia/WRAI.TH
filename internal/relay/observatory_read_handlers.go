package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	obsDefaultLimit = 500
	obsMinLimit     = 1
	obsMaxLimit     = 5000
)

// obsJSON writes v as JSON with Content-Type: application/json.
// Shared by observatory read handlers; T4 ingest handlers may reuse this
// function — do not redefine it in observatory_handlers.go.
func obsJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// obsPathSeg extracts the path segment between prefix and suffix.
// e.g. obsPathSeg("/observatory/api/v1/sessions/abc/events",
//
//	"/observatory/api/v1/sessions/", "/events") → "abc"
func obsPathSeg(path, prefix, suffix string) string {
	s := strings.TrimPrefix(path, prefix)
	return strings.TrimSuffix(s, suffix)
}

// obsLimit parses and validates the ?limit= query parameter.
// Returns (limit, true) on success, or writes 400 and returns (0, false).
func obsLimit(w http.ResponseWriter, req *http.Request) (int, bool) {
	raw := req.URL.Query().Get("limit")
	if raw == "" {
		return obsDefaultLimit, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < obsMinLimit || n > obsMaxLimit {
		obsJSON(w, http.StatusBadRequest, map[string]string{
			"error": "limit must be between 1 and 5000",
		})
		return 0, false
	}
	return n, true
}

// ServeObservatoryRead routes all observatory read (GET) requests.
// T6 is responsible for mounting this behind ObservatoryEasyAuthMiddleware
// and the observatory feature gate.
func (r *Relay) ServeObservatoryRead(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		obsJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if r.PGPool == nil {
		obsJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "observatory unavailable"})
		return
	}

	path := req.URL.Path

	switch {
	// ── collection / aggregate surfaces (exact paths) ──
	case path == "/observatory/api/v1/overview":
		r.serveObsOverview(w, req)

	case path == "/observatory/api/v1/burn":
		r.serveObsBurn(w, req)

	case path == "/observatory/api/v1/budgets":
		r.serveObsBudgets(w, req)

	case path == "/observatory/api/v1/flags/top":
		r.serveObsTopFlags(w, req)

	case path == "/observatory/api/v1/projects":
		r.serveObsProjects(w, req)

	case path == "/observatory/api/v1/agents":
		r.serveObsAgents(w, req)

	// ── projects/{slug} ──
	case obsMatchID(path, "/observatory/api/v1/projects/"):
		r.serveObsProject(w, req)

	// ── agents/{id}/sessions and agents/{slug} ──
	case obsMatchSub(path, "/observatory/api/v1/agents/", "/sessions"):
		r.serveObsAgentSessions(w, req)

	case obsMatchID(path, "/observatory/api/v1/agents/"):
		r.serveObsAgent(w, req)

	// ── sessions/{id}[/events|/token_deltas|/flags] ──
	case obsMatchSub(path, "/observatory/api/v1/sessions/", "/events"):
		r.serveObsSessionEvents(w, req)

	case obsMatchSub(path, "/observatory/api/v1/sessions/", "/token_deltas"):
		r.serveObsSessionTokenDeltas(w, req)

	case obsMatchSub(path, "/observatory/api/v1/sessions/", "/flags"):
		r.serveObsSessionFlags(w, req)

	case obsMatchID(path, "/observatory/api/v1/sessions/"):
		r.serveObsSession(w, req)

	// ── tasks/{id}/[estimates|estimate|runs|run|actuals] ──
	// Plural forms are matched before singular so /estimates and /runs are
	// not shadowed by the /estimate and /run suffix checks.
	case obsMatchSub(path, "/observatory/api/v1/tasks/", "/estimates"):
		r.serveObsTaskEstimates(w, req)

	case obsMatchSub(path, "/observatory/api/v1/tasks/", "/estimate"):
		r.serveObsTaskEstimate(w, req)

	case obsMatchSub(path, "/observatory/api/v1/tasks/", "/runs"):
		r.serveObsTaskRuns(w, req)

	case obsMatchSub(path, "/observatory/api/v1/tasks/", "/run"):
		r.serveObsTaskRun(w, req)

	case obsMatchSub(path, "/observatory/api/v1/tasks/", "/actuals"):
		r.serveObsTaskActuals(w, req)

	default:
		http.NotFound(w, req)
	}
}

// ─── event row ───────────────────────────────────────────────────────────────

type obsEvent struct {
	EventID   int64           `json:"event_id"`
	SessionID string          `json:"session_id"`
	TaskRunID *string         `json:"task_run_id"`
	TraceID   *string         `json:"trace_id"`
	Ts        time.Time       `json:"ts"`
	Kind      string          `json:"kind"`
	Tool      *string         `json:"tool"`
	Path      *string         `json:"path"`
	Input     json.RawMessage `json:"input"`
	Output    json.RawMessage `json:"output"`
	TurnIndex *int            `json:"turn_index"`
}

// GET /observatory/api/v1/sessions/{id}/events
// ?limit=500&after_id=<cursor>   limit: 1–5000, ORDER BY event_id ASC.
func (r *Relay) serveObsSessionEvents(w http.ResponseWriter, req *http.Request) {
	sessionID := obsPathSeg(req.URL.Path, "/observatory/api/v1/sessions/", "/events")
	if sessionID == "" {
		obsJSON(w, http.StatusBadRequest, map[string]string{"error": "missing session id"})
		return
	}

	limit, ok := obsLimit(w, req)
	if !ok {
		return
	}

	var afterID *int64
	if raw := req.URL.Query().Get("after_id"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			obsJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid after_id"})
			return
		}
		afterID = &n
	}

	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()

	var (
		rows pgx.Rows
		err  error
	)
	if afterID == nil {
		rows, err = r.PGPool.Query(ctx, `
			SELECT event_id, session_id, task_run_id, trace_id, ts, kind, tool, path,
			       input, output, turn_index
			  FROM events
			 WHERE session_id = $1
			 ORDER BY event_id ASC
			 LIMIT $2`,
			sessionID, limit)
	} else {
		rows, err = r.PGPool.Query(ctx, `
			SELECT event_id, session_id, task_run_id, trace_id, ts, kind, tool, path,
			       input, output, turn_index
			  FROM events
			 WHERE session_id = $1 AND event_id > $2
			 ORDER BY event_id ASC
			 LIMIT $3`,
			sessionID, *afterID, limit)
	}
	if err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	result := make([]obsEvent, 0)
	for rows.Next() {
		var e obsEvent
		if scanErr := rows.Scan(
			&e.EventID, &e.SessionID, &e.TaskRunID, &e.TraceID,
			&e.Ts, &e.Kind, &e.Tool, &e.Path, &e.Input, &e.Output, &e.TurnIndex,
		); scanErr != nil {
			obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "scan failed"})
			return
		}
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "rows error"})
		return
	}

	obsJSON(w, http.StatusOK, result)
}

// ─── token_delta row ─────────────────────────────────────────────────────────

type obsTokenDelta struct {
	ID                         int64     `json:"id"`
	SessionID                  string    `json:"session_id"`
	TurnIndex                  *int      `json:"turn_index"`
	MessageID                  *string   `json:"message_id"`
	Model                      string    `json:"model"`
	Ts                         time.Time `json:"ts"`
	InputTokens                int       `json:"input_tokens"`
	OutputTokens               int       `json:"output_tokens"`
	CacheReadInputTokens       int       `json:"cache_read_input_tokens"`
	CacheCreationInputTokens5m int       `json:"cache_creation_input_tokens_5m"`
	CacheCreationInputTokens1h int       `json:"cache_creation_input_tokens_1h"`
	CostUSD                    *float64  `json:"cost_usd"`
}

// GET /observatory/api/v1/sessions/{id}/token_deltas
// ?limit=500   limit: 1–5000, ORDER BY id ASC.
func (r *Relay) serveObsSessionTokenDeltas(w http.ResponseWriter, req *http.Request) {
	sessionID := obsPathSeg(req.URL.Path, "/observatory/api/v1/sessions/", "/token_deltas")
	if sessionID == "" {
		obsJSON(w, http.StatusBadRequest, map[string]string{"error": "missing session id"})
		return
	}

	limit, ok := obsLimit(w, req)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()

	rows, err := r.PGPool.Query(ctx, `
		SELECT id, session_id, turn_index, message_id, model, ts,
		       input_tokens, output_tokens, cache_read_input_tokens,
		       cache_creation_input_tokens_5m, cache_creation_input_tokens_1h,
		       cost_usd
		  FROM token_deltas
		 WHERE session_id = $1
		 ORDER BY id ASC
		 LIMIT $2`,
		sessionID, limit)
	if err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	result := make([]obsTokenDelta, 0)
	for rows.Next() {
		var d obsTokenDelta
		if scanErr := rows.Scan(
			&d.ID, &d.SessionID, &d.TurnIndex, &d.MessageID,
			&d.Model, &d.Ts,
			&d.InputTokens, &d.OutputTokens, &d.CacheReadInputTokens,
			&d.CacheCreationInputTokens5m, &d.CacheCreationInputTokens1h,
			&d.CostUSD,
		); scanErr != nil {
			obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "scan failed"})
			return
		}
		result = append(result, d)
	}
	if err := rows.Err(); err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "rows error"})
		return
	}

	obsJSON(w, http.StatusOK, result)
}

// ─── session row ─────────────────────────────────────────────────────────────

type obsSession struct {
	SessionID    string     `json:"session_id"`
	WorkerRunID  string     `json:"worker_run_id"`
	TraceID      *string    `json:"trace_id"`
	SpawnIndex   int        `json:"spawn_index"`
	Model        string     `json:"model"`
	StartedAt    time.Time  `json:"started_at"`
	EndedAt      *time.Time `json:"ended_at"`
	DurationMs   *int64     `json:"duration_ms"`
	Turns        *int       `json:"turns"`
	ExitCode     *int       `json:"exit_code"`
	TotalCostUSD float64    `json:"total_cost_usd"`
	FlagCount    int64      `json:"flag_count"`
	AgentID      *string    `json:"agent_id"`
	ProfileSlug  *string    `json:"profile_slug"`
	ProjectSlug  *string    `json:"project_slug"`
	Mode         *string    `json:"mode"`
}

// GET /observatory/api/v1/sessions/{id}
// Returns a single session row enriched with the owning agent/project, the
// worker-run mode, and the session's cost/flag rollup (from
// mv_session_aggregates); 404 if not found. Mirrors getSession in the retired
// standalone UI's queries.ts so the dashboard's session-detail breadcrumb and
// header cards render.
func (r *Relay) serveObsSession(w http.ResponseWriter, req *http.Request) {
	sessionID := strings.TrimPrefix(req.URL.Path, "/observatory/api/v1/sessions/")
	if sessionID == "" {
		obsJSON(w, http.StatusBadRequest, map[string]string{"error": "missing session id"})
		return
	}

	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()

	var s obsSession
	err := r.PGPool.QueryRow(ctx, `
		SELECT s.session_id, s.worker_run_id, s.trace_id, s.spawn_index, s.model,
		       s.started_at, s.ended_at, s.duration_ms, s.turns, s.exit_code,
		       COALESCE(agg.total_cost_usd, 0) AS total_cost_usd,
		       COALESCE(agg.flag_count, 0)     AS flag_count,
		       COALESCE(s.agent_id, wr.agent_id) AS agent_id,
		       a.profile_slug, a.project_slug, wr.mode
		  FROM sessions s
		  JOIN worker_runs wr ON wr.worker_run_id = s.worker_run_id
		  LEFT JOIN agents a  ON a.agent_id = COALESCE(s.agent_id, wr.agent_id)
		  LEFT JOIN mv_session_aggregates agg ON agg.session_id = s.session_id
		 WHERE s.session_id = $1`,
		sessionID,
	).Scan(
		&s.SessionID, &s.WorkerRunID, &s.TraceID, &s.SpawnIndex, &s.Model,
		&s.StartedAt, &s.EndedAt, &s.DurationMs, &s.Turns, &s.ExitCode,
		&s.TotalCostUSD, &s.FlagCount, &s.AgentID, &s.ProfileSlug, &s.ProjectSlug, &s.Mode,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			obsJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	obsJSON(w, http.StatusOK, s)
}

// ─── task estimate row ───────────────────────────────────────────────────────

type obsTaskEstimate struct {
	TaskID           string          `json:"task_id"`
	EstimatorSource  string          `json:"estimator_source"`
	EstimatedAt      time.Time       `json:"estimated_at"`
	EstimatorAgentID *string         `json:"estimator_agent_id"`
	EstimatorModel   *string         `json:"estimator_model"`
	Complexity       string          `json:"complexity"`
	EstTokensInput   int             `json:"est_tokens_input"`
	EstTokensOutput  int             `json:"est_tokens_output"`
	EstDurationS     int             `json:"est_duration_s"`
	EstFiles         int             `json:"est_files"`
	EstRiskFlags     json.RawMessage `json:"est_risk_flags"`
	Rationale        *string         `json:"rationale"`
}

// GET /observatory/api/v1/tasks/{id}/estimate
// Reads from v_task_estimate (priority-winner per task); 404 if no estimate.
func (r *Relay) serveObsTaskEstimate(w http.ResponseWriter, req *http.Request) {
	taskID := obsPathSeg(req.URL.Path, "/observatory/api/v1/tasks/", "/estimate")
	if taskID == "" {
		obsJSON(w, http.StatusBadRequest, map[string]string{"error": "missing task id"})
		return
	}

	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()

	var e obsTaskEstimate
	err := r.PGPool.QueryRow(ctx, `
		SELECT task_id, estimator_source, estimated_at, estimator_agent_id,
		       estimator_model, complexity, est_tokens_input, est_tokens_output,
		       est_duration_s, est_files, est_risk_flags, rationale
		  FROM v_task_estimate
		 WHERE task_id = $1`,
		taskID,
	).Scan(
		&e.TaskID, &e.EstimatorSource, &e.EstimatedAt, &e.EstimatorAgentID,
		&e.EstimatorModel, &e.Complexity, &e.EstTokensInput, &e.EstTokensOutput,
		&e.EstDurationS, &e.EstFiles, &e.EstRiskFlags, &e.Rationale,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			obsJSON(w, http.StatusNotFound, map[string]string{"error": "no estimate for task"})
			return
		}
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	obsJSON(w, http.StatusOK, e)
}

// ─── task run row ────────────────────────────────────────────────────────────

type obsTaskRun struct {
	TaskRunID        string     `json:"task_run_id"`
	TaskID           string     `json:"task_id"`
	SessionID        string     `json:"session_id"`
	ClaimedAt        *time.Time `json:"claimed_at"`
	StartedAt        *time.Time `json:"started_at"`
	CompletedAt      *time.Time `json:"completed_at"`
	TerminalState    *string    `json:"terminal_state"`
	OutcomeSummary   *string    `json:"outcome_summary"`
	ForecastAccuracy *float64   `json:"forecast_accuracy"`
}

// GET /observatory/api/v1/tasks/{id}/run
// Returns the most recent task_runs row for the task; 404 if none.
func (r *Relay) serveObsTaskRun(w http.ResponseWriter, req *http.Request) {
	taskID := obsPathSeg(req.URL.Path, "/observatory/api/v1/tasks/", "/run")
	if taskID == "" {
		obsJSON(w, http.StatusBadRequest, map[string]string{"error": "missing task id"})
		return
	}

	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()

	var tr obsTaskRun
	err := r.PGPool.QueryRow(ctx, `
		SELECT task_run_id, task_id, session_id, claimed_at, started_at,
		       completed_at, terminal_state, outcome_summary, forecast_accuracy
		  FROM task_runs
		 WHERE task_id = $1
		 ORDER BY COALESCE(completed_at, started_at, claimed_at) DESC NULLS LAST
		 LIMIT 1`,
		taskID,
	).Scan(
		&tr.TaskRunID, &tr.TaskID, &tr.SessionID, &tr.ClaimedAt, &tr.StartedAt,
		&tr.CompletedAt, &tr.TerminalState, &tr.OutcomeSummary, &tr.ForecastAccuracy,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			obsJSON(w, http.StatusNotFound, map[string]string{"error": "no run for task"})
			return
		}
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	obsJSON(w, http.StatusOK, tr)
}
