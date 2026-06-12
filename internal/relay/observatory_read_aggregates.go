package relay

// Observatory aggregate / list read handlers.
//
// These GET endpoints back the embedded observatory dashboard SPA
// (internal/web/static/observatory, served at /observatory/). They are the
// in-relay port of the queries that the retired standalone observatory-ui
// (agt-geonosis/sources/observatory/ui/lib/queries.ts) used to run directly
// against Postgres. Every handler queries r.PGPool and is mounted behind
// ObservatoryEasyAuthMiddleware by ListenAndServe.
//
// The drill-down reads (single session, session events/token_deltas, single
// task estimate/run) live in observatory_read_handlers.go; this file adds the
// list and aggregate surfaces (overview, burn, projects, agents, budgets,
// flags, task runs/estimates/actuals).

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// obsWindow parses the ?window= query parameter, constraining it to the
// dashboard's supported buckets (1, 7, 30 days). Anything else falls back to 7.
//
// The result is bound into SQL as `$1::int * interval '1 day'` (NOT the
// `$1 || ' days'` form the retired Node UI used): with `$1 || ' days'` the
// server infers $1 as text, and pgx then refuses to encode a Go int into a
// text parameter ("cannot find encode plan"). The explicit ::int keeps $1 as
// int4 so pgx encodes it natively. Do not revert to string concatenation.
func obsWindow(req *http.Request) int {
	switch req.URL.Query().Get("window") {
	case "1":
		return 1
	case "30":
		return 30
	default:
		return 7
	}
}

// obsCtx returns a request-scoped context with the standard 10s read timeout.
func obsCtx(req *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(req.Context(), 10*time.Second)
}

// ─── overview ──────────────────────────────────────────────────────────────

type obsOverview struct {
	TodayCostUSD       float64  `json:"today_cost_usd"`
	TodayInputTokens   int64    `json:"today_input_tokens"`
	TodayOutputTokens  int64    `json:"today_output_tokens"`
	ActiveAgents       int64    `json:"active_agents"`
	TasksInFlight      int64    `json:"tasks_in_flight"`
	ForecastAccuracy7d *float64 `json:"forecast_accuracy_7d"`
}

// GET /observatory/api/v1/overview
func (r *Relay) serveObsOverview(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := obsCtx(req)
	defer cancel()

	var o obsOverview
	err := r.PGPool.QueryRow(ctx, `
		WITH today AS (
		  SELECT COALESCE(SUM(td.cost_usd), 0)      AS today_cost_usd,
		         COALESCE(SUM(td.input_tokens), 0)  AS today_input_tokens,
		         COALESCE(SUM(td.output_tokens), 0) AS today_output_tokens
		    FROM token_deltas td
		   WHERE td.ts >= date_trunc('day', now())
		),
		active AS (
		  SELECT COUNT(DISTINCT wr.agent_id) AS active_agents
		    FROM worker_runs wr
		   WHERE wr.ended_at IS NULL
		      OR wr.ended_at >= now() - interval '15 minutes'
		),
		in_flight AS (
		  SELECT COUNT(*) AS tasks_in_flight
		    FROM task_runs
		   WHERE claimed_at IS NOT NULL
		     AND completed_at IS NULL
		),
		accuracy AS (
		  SELECT AVG(forecast_accuracy) AS forecast_accuracy_7d
		    FROM task_runs
		   WHERE forecast_accuracy IS NOT NULL
		     AND completed_at >= now() - interval '7 days'
		)
		SELECT today.today_cost_usd, today.today_input_tokens, today.today_output_tokens,
		       active.active_agents, in_flight.tasks_in_flight, accuracy.forecast_accuracy_7d
		  FROM today, active, in_flight, accuracy`,
	).Scan(
		&o.TodayCostUSD, &o.TodayInputTokens, &o.TodayOutputTokens,
		&o.ActiveAgents, &o.TasksInFlight, &o.ForecastAccuracy7d,
	)
	if err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	obsJSON(w, http.StatusOK, o)
}

// ─── burn by project ─────────────────────────────────────────────────────────

type obsBurnRow struct {
	DayBucket       time.Time `json:"day_bucket"`
	ProjectSlug     *string   `json:"project_slug"`
	Model           string    `json:"model"`
	InputTokens     int64     `json:"input_tokens"`
	OutputTokens    int64     `json:"output_tokens"`
	CacheRead       int64     `json:"cache_read_input_tokens"`
	CacheCreation5m int64     `json:"cache_creation_5m"`
	CacheCreation1h int64     `json:"cache_creation_1h"`
	CostUSD         float64   `json:"cost_usd"`
}

// GET /observatory/api/v1/burn?window=7
func (r *Relay) serveObsBurn(w http.ResponseWriter, req *http.Request) {
	window := obsWindow(req)
	ctx, cancel := obsCtx(req)
	defer cancel()

	rows, err := r.PGPool.Query(ctx, `
		SELECT day_bucket, project_slug, model,
		       input_tokens, output_tokens, cache_read_input_tokens,
		       cache_creation_5m, cache_creation_1h,
		       COALESCE(cost_usd, 0)
		  FROM mv_daily_burn_by_project
		 WHERE day_bucket >= (now() - ($1::int * interval '1 day'))::date
		 ORDER BY day_bucket DESC, cost_usd DESC NULLS LAST`,
		window,
	)
	if err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	result := make([]obsBurnRow, 0)
	for rows.Next() {
		var b obsBurnRow
		// '<unattributed>' is the MV's sentinel for sessions with no agent
		// link; surface it as null so the SPA renders "(unattached)".
		var slug *string
		if scanErr := rows.Scan(
			&b.DayBucket, &slug, &b.Model,
			&b.InputTokens, &b.OutputTokens, &b.CacheRead,
			&b.CacheCreation5m, &b.CacheCreation1h, &b.CostUSD,
		); scanErr != nil {
			obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "scan failed"})
			return
		}
		if slug != nil && *slug != "<unattributed>" {
			b.ProjectSlug = slug
		}
		result = append(result, b)
	}
	if err := rows.Err(); err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "rows error"})
		return
	}
	obsJSON(w, http.StatusOK, result)
}

// ─── projects ────────────────────────────────────────────────────────────────

type obsProject struct {
	Slug        string  `json:"slug"`
	GithubRepo  *string `json:"github_repo"`
	VaultShare  *string `json:"vault_share"`
	AgentCount  int64   `json:"agent_count"`
	TaskCount7d int64   `json:"task_count_7d"`
}

const obsProjectSelect = `
	SELECT p.slug, p.github_repo, p.vault_share,
	       COUNT(DISTINCT a.agent_id) AS agent_count,
	       COUNT(DISTINCT tr.task_id) FILTER (
	         WHERE tr.claimed_at >= now() - interval '7 days'
	       ) AS task_count_7d
	  FROM projects p
	  LEFT JOIN agents a    ON a.project_slug = p.slug
	  LEFT JOIN sessions s  ON s.agent_id = a.agent_id
	  LEFT JOIN task_runs tr ON tr.session_id = s.session_id`

func scanObsProject(row pgx.Row, p *obsProject) error {
	return row.Scan(&p.Slug, &p.GithubRepo, &p.VaultShare, &p.AgentCount, &p.TaskCount7d)
}

// GET /observatory/api/v1/projects
func (r *Relay) serveObsProjects(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := obsCtx(req)
	defer cancel()

	rows, err := r.PGPool.Query(ctx, obsProjectSelect+`
		 GROUP BY p.slug, p.github_repo, p.vault_share
		 ORDER BY p.slug`)
	if err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	result := make([]obsProject, 0)
	for rows.Next() {
		var p obsProject
		if scanErr := scanObsProject(rows, &p); scanErr != nil {
			obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "scan failed"})
			return
		}
		result = append(result, p)
	}
	if err := rows.Err(); err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "rows error"})
		return
	}
	obsJSON(w, http.StatusOK, result)
}

// GET /observatory/api/v1/projects/{slug}
func (r *Relay) serveObsProject(w http.ResponseWriter, req *http.Request) {
	slug := strings.TrimPrefix(req.URL.Path, "/observatory/api/v1/projects/")
	if slug == "" || strings.Contains(slug, "/") {
		obsJSON(w, http.StatusBadRequest, map[string]string{"error": "missing project slug"})
		return
	}
	ctx, cancel := obsCtx(req)
	defer cancel()

	var p obsProject
	err := scanObsProject(r.PGPool.QueryRow(ctx, obsProjectSelect+`
		 WHERE p.slug = $1
		 GROUP BY p.slug, p.github_repo, p.vault_share`, slug), &p)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			obsJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
			return
		}
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	obsJSON(w, http.StatusOK, p)
}

// ─── agents ──────────────────────────────────────────────────────────────────

type obsAgent struct {
	AgentID        string     `json:"agent_id"`
	ProfileSlug    string     `json:"profile_slug"`
	ProjectSlug    *string    `json:"project_slug"`
	Role           *string    `json:"role"`
	Model          *string    `json:"model"`
	CreatedAt      time.Time  `json:"created_at"`
	LastSessionAt  *time.Time `json:"last_session_at"`
	SessionCount7d int64      `json:"session_count_7d"`
}

const obsAgentSelect = `
	SELECT a.agent_id, a.profile_slug, a.project_slug, a.role, a.model,
	       a.created_at,
	       MAX(s.started_at) AS last_session_at,
	       COUNT(s.session_id) FILTER (
	         WHERE s.started_at >= now() - interval '7 days'
	       ) AS session_count_7d
	  FROM agents a
	  LEFT JOIN sessions s ON s.agent_id = a.agent_id`

const obsAgentGroupOrder = `
	 GROUP BY a.agent_id, a.profile_slug, a.project_slug, a.role, a.model, a.created_at
	 ORDER BY MAX(s.started_at) DESC NULLS LAST, a.agent_id`

func scanObsAgent(row pgx.Row, a *obsAgent) error {
	return row.Scan(&a.AgentID, &a.ProfileSlug, &a.ProjectSlug, &a.Role, &a.Model,
		&a.CreatedAt, &a.LastSessionAt, &a.SessionCount7d)
}

// GET /observatory/api/v1/agents?project={slug}
func (r *Relay) serveObsAgents(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := obsCtx(req)
	defer cancel()

	var (
		rows pgx.Rows
		err  error
	)
	if project := req.URL.Query().Get("project"); project != "" {
		rows, err = r.PGPool.Query(ctx, obsAgentSelect+`
			 WHERE a.project_slug = $1`+obsAgentGroupOrder, project)
	} else {
		rows, err = r.PGPool.Query(ctx, obsAgentSelect+obsAgentGroupOrder)
	}
	if err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	result := make([]obsAgent, 0)
	for rows.Next() {
		var a obsAgent
		if scanErr := scanObsAgent(rows, &a); scanErr != nil {
			obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "scan failed"})
			return
		}
		result = append(result, a)
	}
	if err := rows.Err(); err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "rows error"})
		return
	}
	obsJSON(w, http.StatusOK, result)
}

// GET /observatory/api/v1/agents/{slug}
// {slug} matches either agent_id or profile_slug (mirrors the standalone UI).
func (r *Relay) serveObsAgent(w http.ResponseWriter, req *http.Request) {
	slug := strings.TrimPrefix(req.URL.Path, "/observatory/api/v1/agents/")
	if slug == "" || strings.Contains(slug, "/") {
		obsJSON(w, http.StatusBadRequest, map[string]string{"error": "missing agent slug"})
		return
	}
	ctx, cancel := obsCtx(req)
	defer cancel()

	var a obsAgent
	err := scanObsAgent(r.PGPool.QueryRow(ctx, obsAgentSelect+`
		 WHERE a.agent_id = $1 OR a.profile_slug = $1
		 GROUP BY a.agent_id, a.profile_slug, a.project_slug, a.role, a.model, a.created_at
		 ORDER BY MAX(s.started_at) DESC NULLS LAST
		 LIMIT 1`, slug), &a)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			obsJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
			return
		}
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	obsJSON(w, http.StatusOK, a)
}

// ─── sessions for agent ──────────────────────────────────────────────────────

type obsSessionListRow struct {
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
}

// GET /observatory/api/v1/agents/{id}/sessions?limit=50
func (r *Relay) serveObsAgentSessions(w http.ResponseWriter, req *http.Request) {
	agentID := obsPathSeg(req.URL.Path, "/observatory/api/v1/agents/", "/sessions")
	if agentID == "" {
		obsJSON(w, http.StatusBadRequest, map[string]string{"error": "missing agent id"})
		return
	}
	limit, ok := obsLimit(w, req)
	if !ok {
		return
	}
	ctx, cancel := obsCtx(req)
	defer cancel()

	rows, err := r.PGPool.Query(ctx, `
		SELECT s.session_id, s.worker_run_id, s.trace_id, s.spawn_index, s.model,
		       s.started_at, s.ended_at, s.duration_ms, s.turns, s.exit_code,
		       COALESCE(agg.total_cost_usd, 0) AS total_cost_usd,
		       COALESCE(agg.flag_count, 0)     AS flag_count
		  FROM sessions s
		  LEFT JOIN mv_session_aggregates agg ON agg.session_id = s.session_id
		 WHERE s.agent_id = $1
		 ORDER BY s.started_at DESC
		 LIMIT $2`,
		agentID, limit,
	)
	if err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	result := make([]obsSessionListRow, 0)
	for rows.Next() {
		var s obsSessionListRow
		if scanErr := rows.Scan(
			&s.SessionID, &s.WorkerRunID, &s.TraceID, &s.SpawnIndex, &s.Model,
			&s.StartedAt, &s.EndedAt, &s.DurationMs, &s.Turns, &s.ExitCode,
			&s.TotalCostUSD, &s.FlagCount,
		); scanErr != nil {
			obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "scan failed"})
			return
		}
		result = append(result, s)
	}
	if err := rows.Err(); err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "rows error"})
		return
	}
	obsJSON(w, http.StatusOK, result)
}

// ─── flags ───────────────────────────────────────────────────────────────────

type obsFlag struct {
	FlagID     int64     `json:"flag_id"`
	EventID    int64     `json:"event_id"`
	RuleID     string    `json:"rule_id"`
	Severity   string    `json:"severity"`
	CapturedAt time.Time `json:"captured_at"`
}

// GET /observatory/api/v1/sessions/{id}/flags
func (r *Relay) serveObsSessionFlags(w http.ResponseWriter, req *http.Request) {
	sessionID := obsPathSeg(req.URL.Path, "/observatory/api/v1/sessions/", "/flags")
	if sessionID == "" {
		obsJSON(w, http.StatusBadRequest, map[string]string{"error": "missing session id"})
		return
	}
	ctx, cancel := obsCtx(req)
	defer cancel()

	rows, err := r.PGPool.Query(ctx, `
		SELECT flag_id, event_id, rule_id, severity, captured_at
		  FROM flags
		 WHERE session_id = $1
		 ORDER BY flag_id ASC`,
		sessionID,
	)
	if err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	result := make([]obsFlag, 0)
	for rows.Next() {
		var f obsFlag
		if scanErr := rows.Scan(&f.FlagID, &f.EventID, &f.RuleID, &f.Severity, &f.CapturedAt); scanErr != nil {
			obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "scan failed"})
			return
		}
		result = append(result, f)
	}
	if err := rows.Err(); err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "rows error"})
		return
	}
	obsJSON(w, http.StatusOK, result)
}

type obsTopFlag struct {
	RuleID   string `json:"rule_id"`
	Severity string `json:"severity"`
	Count    int64  `json:"count"`
}

// GET /observatory/api/v1/flags/top?window=7
func (r *Relay) serveObsTopFlags(w http.ResponseWriter, req *http.Request) {
	window := obsWindow(req)
	ctx, cancel := obsCtx(req)
	defer cancel()

	rows, err := r.PGPool.Query(ctx, `
		SELECT rule_id, severity, COUNT(*) AS count
		  FROM flags
		 WHERE captured_at >= now() - ($1::int * interval '1 day')
		 GROUP BY rule_id, severity
		 ORDER BY COUNT(*) DESC
		 LIMIT 20`,
		window,
	)
	if err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	result := make([]obsTopFlag, 0)
	for rows.Next() {
		var f obsTopFlag
		if scanErr := rows.Scan(&f.RuleID, &f.Severity, &f.Count); scanErr != nil {
			obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "scan failed"})
			return
		}
		result = append(result, f)
	}
	if err := rows.Err(); err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "rows error"})
		return
	}
	obsJSON(w, http.StatusOK, result)
}

// ─── budgets ─────────────────────────────────────────────────────────────────

type obsBudget struct {
	ProfileSlug       string  `json:"profile_slug"`
	AgentCount        int64   `json:"agent_count"`
	DailyInputTokens  int64   `json:"daily_input_tokens"`
	DailyOutputTokens int64   `json:"daily_output_tokens"`
	DailyCostUSD      float64 `json:"daily_cost_usd"`
}

// GET /observatory/api/v1/budgets
func (r *Relay) serveObsBudgets(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := obsCtx(req)
	defer cancel()

	rows, err := r.PGPool.Query(ctx, `
		SELECT a.profile_slug,
		       COUNT(DISTINCT a.agent_id)            AS agent_count,
		       COALESCE(SUM(td.input_tokens), 0)     AS daily_input_tokens,
		       COALESCE(SUM(td.output_tokens), 0)    AS daily_output_tokens,
		       COALESCE(SUM(td.cost_usd), 0)         AS daily_cost_usd
		  FROM agents a
		  LEFT JOIN sessions s ON s.agent_id = a.agent_id
		  LEFT JOIN token_deltas td
		         ON td.session_id = s.session_id
		        AND td.ts >= date_trunc('day', now())
		 GROUP BY a.profile_slug
		 ORDER BY daily_cost_usd DESC NULLS LAST`,
	)
	if err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	result := make([]obsBudget, 0)
	for rows.Next() {
		var b obsBudget
		if scanErr := rows.Scan(
			&b.ProfileSlug, &b.AgentCount, &b.DailyInputTokens, &b.DailyOutputTokens, &b.DailyCostUSD,
		); scanErr != nil {
			obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "scan failed"})
			return
		}
		result = append(result, b)
	}
	if err := rows.Err(); err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "rows error"})
		return
	}
	obsJSON(w, http.StatusOK, result)
}

// ─── task runs / estimates / actuals ─────────────────────────────────────────

// GET /observatory/api/v1/tasks/{id}/runs
// Returns all task_runs rows for the task (the singular /run returns only the
// most recent). Reuses the obsTaskRun shape from observatory_read_handlers.go.
func (r *Relay) serveObsTaskRuns(w http.ResponseWriter, req *http.Request) {
	taskID := obsPathSeg(req.URL.Path, "/observatory/api/v1/tasks/", "/runs")
	if taskID == "" {
		obsJSON(w, http.StatusBadRequest, map[string]string{"error": "missing task id"})
		return
	}
	ctx, cancel := obsCtx(req)
	defer cancel()

	rows, err := r.PGPool.Query(ctx, `
		SELECT task_run_id, task_id, session_id, claimed_at, started_at,
		       completed_at, terminal_state, outcome_summary, forecast_accuracy
		  FROM task_runs
		 WHERE task_id = $1
		 ORDER BY COALESCE(completed_at, started_at, claimed_at) DESC NULLS LAST`,
		taskID,
	)
	if err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	result := make([]obsTaskRun, 0)
	for rows.Next() {
		var tr obsTaskRun
		if scanErr := rows.Scan(
			&tr.TaskRunID, &tr.TaskID, &tr.SessionID, &tr.ClaimedAt, &tr.StartedAt,
			&tr.CompletedAt, &tr.TerminalState, &tr.OutcomeSummary, &tr.ForecastAccuracy,
		); scanErr != nil {
			obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "scan failed"})
			return
		}
		result = append(result, tr)
	}
	if err := rows.Err(); err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "rows error"})
		return
	}
	obsJSON(w, http.StatusOK, result)
}

// GET /observatory/api/v1/tasks/{id}/estimates
// Returns every estimator's row (dispatcher_envelope + executor_first_turn),
// priority-ordered. The singular /estimate returns only the v_task_estimate
// winner.
func (r *Relay) serveObsTaskEstimates(w http.ResponseWriter, req *http.Request) {
	taskID := obsPathSeg(req.URL.Path, "/observatory/api/v1/tasks/", "/estimates")
	if taskID == "" {
		obsJSON(w, http.StatusBadRequest, map[string]string{"error": "missing task id"})
		return
	}
	ctx, cancel := obsCtx(req)
	defer cancel()

	rows, err := r.PGPool.Query(ctx, `
		SELECT task_id, estimator_source, estimated_at, estimator_agent_id,
		       estimator_model, complexity, est_tokens_input, est_tokens_output,
		       est_duration_s, est_files, est_risk_flags, rationale
		  FROM task_estimates
		 WHERE task_id = $1
		 ORDER BY CASE estimator_source
		              WHEN 'dispatcher_envelope' THEN 0
		              WHEN 'executor_first_turn' THEN 1
		              ELSE 9
		          END`,
		taskID,
	)
	if err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	result := make([]obsTaskEstimate, 0)
	for rows.Next() {
		var e obsTaskEstimate
		if scanErr := rows.Scan(
			&e.TaskID, &e.EstimatorSource, &e.EstimatedAt, &e.EstimatorAgentID,
			&e.EstimatorModel, &e.Complexity, &e.EstTokensInput, &e.EstTokensOutput,
			&e.EstDurationS, &e.EstFiles, &e.EstRiskFlags, &e.Rationale,
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

type obsTaskActuals struct {
	TaskID            string `json:"task_id"`
	TotalInputTokens  int64  `json:"total_input_tokens"`
	TotalOutputTokens int64  `json:"total_output_tokens"`
	TotalDurationS    int64  `json:"total_duration_s"`
	TotalFilesTouched int64  `json:"total_files_touched"`
}

// GET /observatory/api/v1/tasks/{id}/actuals
func (r *Relay) serveObsTaskActuals(w http.ResponseWriter, req *http.Request) {
	taskID := obsPathSeg(req.URL.Path, "/observatory/api/v1/tasks/", "/actuals")
	if taskID == "" {
		obsJSON(w, http.StatusBadRequest, map[string]string{"error": "missing task id"})
		return
	}
	ctx, cancel := obsCtx(req)
	defer cancel()

	a := obsTaskActuals{TaskID: taskID}
	err := r.PGPool.QueryRow(ctx, `
		WITH runs AS (
		  SELECT task_run_id, task_id, session_id, claimed_at, completed_at
		    FROM task_runs
		   WHERE task_id = $1
		),
		tokens AS (
		  SELECT SUM(td.input_tokens)  AS in_tok,
		         SUM(td.output_tokens) AS out_tok
		    FROM runs
		    JOIN token_deltas td ON td.session_id = runs.session_id
		),
		duration AS (
		  SELECT SUM(EXTRACT(EPOCH FROM (
		           COALESCE(runs.completed_at, now()) - runs.claimed_at
		         )))::bigint AS dur_s
		    FROM runs
		   WHERE runs.claimed_at IS NOT NULL
		),
		files AS (
		  SELECT COUNT(DISTINCT e.path) AS file_count
		    FROM runs
		    JOIN events e ON e.task_run_id = runs.task_run_id
		   WHERE e.tool IN ('Edit','Write','MultiEdit','NotebookEdit')
		     AND e.path IS NOT NULL
		)
		SELECT COALESCE(tokens.in_tok, 0),
		       COALESCE(tokens.out_tok, 0),
		       COALESCE(duration.dur_s, 0),
		       COALESCE(files.file_count, 0)
		  FROM (VALUES (1)) t(x)
		  LEFT JOIN tokens   ON true
		  LEFT JOIN duration ON true
		  LEFT JOIN files    ON true`,
		taskID,
	).Scan(&a.TotalInputTokens, &a.TotalOutputTokens, &a.TotalDurationS, &a.TotalFilesTouched)
	if err != nil {
		obsJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	obsJSON(w, http.StatusOK, a)
}
