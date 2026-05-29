package relay

// Observatory ingest handlers — POST endpoints for the /observatory/api/v1/ingest/* routes.
//
// Wire contract is byte-compatible with the agt-geonosis FastAPI observatory
// (sources/observatory/api/src/adf_observatory_api/). Handlers are exported so
// T6 can mount them under the observatoryMux without touching this file.
//
// No auth on these routes (intentional — trust via ACA internal networking per ADF-083).
// Route mounting is T6 scope; do NOT wire into relay.go in this PR.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---- Wire-in types (field names match Python Pydantic models verbatim) ----

type workerRunRegisterIn struct {
	WorkerRunID       string    `json:"worker_run_id"`
	AgentID           string    `json:"agent_id"`
	TraceID           *string   `json:"trace_id"`
	StartedAt         time.Time `json:"started_at"`
	Mode              string    `json:"mode"`
	ContainerRevision *string   `json:"container_revision"`
	ProfileSlug       *string   `json:"profile_slug"`
	ProjectSlug       *string   `json:"project_slug"`
}

type sessionRegisterIn struct {
	SessionID   string    `json:"session_id"`
	WorkerRunID string    `json:"worker_run_id"`
	TraceID     *string   `json:"trace_id"`
	SpawnIndex  int       `json:"spawn_index"`
	Model       string    `json:"model"`
	StartedAt   time.Time `json:"started_at"`
	ProfileSlug *string   `json:"profile_slug"`
	ProjectSlug *string   `json:"project_slug"`
}

type sessionFinalizeIn struct {
	SessionID  string    `json:"session_id"`
	EndedAt    time.Time `json:"ended_at"`
	DurationMS int64     `json:"duration_ms"`
	Turns      int       `json:"turns"`
	ExitCode   *int      `json:"exit_code"`
}

type obsEventIn struct {
	SessionID string    `json:"session_id"`
	TaskRunID *string   `json:"task_run_id"`
	TraceID   *string   `json:"trace_id"`
	TS        time.Time `json:"ts"`
	Kind      string    `json:"kind"`
	Tool      *string   `json:"tool"`
	Path      *string   `json:"path"`
	Input     any       `json:"input"`
	Output    any       `json:"output"`
	TurnIndex *int      `json:"turn_index"`
}

type obsTokenDeltaIn struct {
	SessionID                  string    `json:"session_id"`
	TurnIndex                  *int      `json:"turn_index"`
	MessageID                  *string   `json:"message_id"`
	Model                      string    `json:"model"`
	TS                         time.Time `json:"ts"`
	InputTokens                int       `json:"input_tokens"`
	OutputTokens               int       `json:"output_tokens"`
	CacheReadInputTokens       int       `json:"cache_read_input_tokens"`
	CacheCreationInputTokens5m int       `json:"cache_creation_input_tokens_5m"`
	CacheCreationInputTokens1h int       `json:"cache_creation_input_tokens_1h"`
}

type ingestBatchIn struct {
	Events      []obsEventIn      `json:"events"`
	TokenDeltas []obsTokenDeltaIn `json:"token_deltas"`
}

type obsTaskEstimateIn struct {
	TaskID           string    `json:"task_id"`
	EstimatorSource  string    `json:"estimator_source"`
	EstimatorAgentID *string   `json:"estimator_agent_id"`
	EstimatorModel   *string   `json:"estimator_model"`
	EstimatedAt      time.Time `json:"estimated_at"`
	Complexity       string    `json:"complexity"`
	EstTokensInput   int       `json:"est_tokens_input"`
	EstTokensOutput  int       `json:"est_tokens_output"`
	EstDurationS     int       `json:"est_duration_s"`
	EstFiles         int       `json:"est_files"`
	EstRiskFlags     []string  `json:"est_risk_flags"`
	Rationale        *string   `json:"rationale"`
}

type taskRunUpsertIn struct {
	TaskRunID      string     `json:"task_run_id"`
	TaskID         string     `json:"task_id"`
	SessionID      string     `json:"session_id"`
	ClaimedAt      *time.Time `json:"claimed_at"`
	StartedAt      *time.Time `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	TerminalState  *string    `json:"terminal_state"`
	OutcomeSummary *string    `json:"outcome_summary"`
}

// ---- Severity registry for secret scrubber rules ----
// Mirrors agt-geonosis rules.py SEVERITY_BY_RULE for the secret_* pack.

var secretRuleSeverity = map[string]string{
	"secret_aws_access_key_id":     "high",
	"secret_aws_secret_access_key": "high",
	"secret_stripe_key":            "high",
	"secret_jwt":                   "high",
	"secret_ssh_private_key":       "high",
	"secret_github_pat":            "high",
	"secret_anthropic_key":         "high",
}

func obsSeverityFor(ruleID string) string {
	if s, ok := secretRuleSeverity[ruleID]; ok {
		return s
	}
	return "medium"
}

// ---- Profile-slug helpers (ported from agt-geonosis ingest.py) ----

// roleSuffixes is ordered longest-first so "tech-lead" wins over single-word roles.
var obsRoleSuffixes = []string{
	"tech-lead", "cto", "architect", "backend", "frontend", "devops", "qa", "sre",
}

const obsTransversalSuffix = "-transversal"

// obsDeriveProjSlug extracts the project slug from a <project>-<role> profile slug.
// Returns nil for pool workers, transversal singletons, or unrecognised shapes.
func obsDeriveProjSlug(profileSlug *string) *string {
	if profileSlug == nil || *profileSlug == "" {
		return nil
	}
	s := *profileSlug
	if strings.HasSuffix(s, obsTransversalSuffix) {
		return nil
	}
	for _, suffix := range obsRoleSuffixes {
		token := "-" + suffix
		if strings.HasSuffix(s, token) {
			head := s[:len(s)-len(token)]
			if head == "" {
				return nil
			}
			return &head
		}
	}
	return nil
}

// obsDeriveRole picks the role suffix from a profile slug, if it matches a known one.
func obsDeriveRole(profileSlug *string) *string {
	if profileSlug == nil || *profileSlug == "" {
		return nil
	}
	s := *profileSlug
	if strings.HasSuffix(s, obsTransversalSuffix) {
		head := s[:len(s)-len(obsTransversalSuffix)]
		for _, r := range obsRoleSuffixes {
			if head == r {
				role := head
				return &role
			}
		}
		return nil
	}
	for _, suffix := range obsRoleSuffixes {
		if s == suffix || strings.HasSuffix(s, "-"+suffix) {
			role := suffix
			return &role
		}
	}
	return nil
}

// ---- DB helpers ----

func obsUpsertProject(ctx context.Context, pool *pgxpool.Pool, slug *string) error {
	if slug == nil {
		return nil
	}
	_, err := pool.Exec(ctx,
		"INSERT INTO projects (slug) VALUES ($1) ON CONFLICT (slug) DO NOTHING",
		*slug,
	)
	return err
}

// obsUpsertAgent idempotently inserts an agents row; richer metadata upgrades
// the row on subsequent calls (profile_slug/project_slug/role via DO UPDATE).
func obsUpsertAgent(ctx context.Context, pool *pgxpool.Pool, agentID string, profileSlug, projectSlug, role *string) error {
	effective := agentID
	if profileSlug != nil && *profileSlug != "" {
		effective = *profileSlug
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO agents (agent_id, profile_slug, project_slug, role)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (agent_id) DO UPDATE
		   SET profile_slug = EXCLUDED.profile_slug,
		       project_slug = COALESCE(EXCLUDED.project_slug, agents.project_slug),
		       role         = COALESCE(EXCLUDED.role,         agents.role)
	`, agentID, effective, projectSlug, role)
	return err
}

// obsJSONB marshals v to a JSON string pointer for $N::jsonb SQL params.
// Returns nil (→ SQL NULL) when v is nil.
func obsJSONB(v any) *string {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}

// obsWrite sends a JSON response.
func obsWrite(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// obsCheckPool returns false and writes 503 if ObservatoryDB is not available.
func obsCheckPool(r *Relay, w http.ResponseWriter) bool {
	if r.ObservatoryDB == nil {
		obsWrite(w, http.StatusServiceUnavailable, map[string]string{"error": "observatory unavailable"})
		return false
	}
	return true
}

// ---- Handlers ----

// ObsIngestWorkerRuns handles POST /observatory/api/v1/ingest/worker_runs.
func (r *Relay) ObsIngestWorkerRuns(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.NotFound(w, req)
		return
	}
	if !obsCheckPool(r, w) {
		return
	}
	var p workerRunRegisterIn
	if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
		obsWrite(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}

	ctx := req.Context()
	projSlug := p.ProjectSlug
	if projSlug == nil {
		projSlug = obsDeriveProjSlug(p.ProfileSlug)
	}
	if err := obsUpsertProject(ctx, r.ObservatoryDB, projSlug); err != nil {
		log.Printf("obs/worker_runs: upsert project: %v", err)
		obsWrite(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if err := obsUpsertAgent(ctx, r.ObservatoryDB, p.AgentID, p.ProfileSlug, projSlug, obsDeriveRole(p.ProfileSlug)); err != nil {
		log.Printf("obs/worker_runs: upsert agent: %v", err)
		obsWrite(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if _, err := r.ObservatoryDB.Exec(ctx, `
		INSERT INTO worker_runs (worker_run_id, agent_id, trace_id, started_at, mode, container_revision)
		VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6)
		ON CONFLICT (worker_run_id) DO UPDATE
		   SET trace_id           = EXCLUDED.trace_id,
		       container_revision = EXCLUDED.container_revision
	`, p.WorkerRunID, p.AgentID, p.TraceID, p.StartedAt, p.Mode, p.ContainerRevision); err != nil {
		log.Printf("obs/worker_runs: insert: %v", err)
		obsWrite(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	obsWrite(w, http.StatusAccepted, map[string]string{"status": "registered"})
}

// ObsIngestSessions handles POST /observatory/api/v1/ingest/sessions.
func (r *Relay) ObsIngestSessions(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.NotFound(w, req)
		return
	}
	if !obsCheckPool(r, w) {
		return
	}
	var p sessionRegisterIn
	if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
		obsWrite(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}

	ctx := req.Context()
	// Per-task persona upsert: when profile_slug is known, link the session to the
	// persona agents row so downstream rollups attribute work to the right project.
	var sessionAgentID *string
	if p.ProfileSlug != nil && *p.ProfileSlug != "" {
		projSlug := p.ProjectSlug
		if projSlug == nil {
			projSlug = obsDeriveProjSlug(p.ProfileSlug)
		}
		if err := obsUpsertProject(ctx, r.ObservatoryDB, projSlug); err != nil {
			log.Printf("obs/sessions: upsert project: %v", err)
			obsWrite(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		// Use profile_slug as the persona's agent_id (separate row from the pool worker).
		if err := obsUpsertAgent(ctx, r.ObservatoryDB, *p.ProfileSlug, p.ProfileSlug, projSlug, obsDeriveRole(p.ProfileSlug)); err != nil {
			log.Printf("obs/sessions: upsert agent: %v", err)
			obsWrite(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		sessionAgentID = p.ProfileSlug
	}
	if _, err := r.ObservatoryDB.Exec(ctx, `
		INSERT INTO sessions (session_id, worker_run_id, agent_id, trace_id, spawn_index, model, started_at)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7)
		ON CONFLICT (session_id) DO UPDATE
		   SET agent_id = COALESCE(EXCLUDED.agent_id, sessions.agent_id)
	`, p.SessionID, p.WorkerRunID, sessionAgentID, p.TraceID, p.SpawnIndex, p.Model, p.StartedAt); err != nil {
		log.Printf("obs/sessions: insert: %v", err)
		obsWrite(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	obsWrite(w, http.StatusAccepted, map[string]string{"status": "registered"})
}

// ObsIngestSessionsFinalize handles POST /observatory/api/v1/ingest/sessions/finalize.
func (r *Relay) ObsIngestSessionsFinalize(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.NotFound(w, req)
		return
	}
	if !obsCheckPool(r, w) {
		return
	}
	var p sessionFinalizeIn
	if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
		obsWrite(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	if _, err := r.ObservatoryDB.Exec(req.Context(), `
		UPDATE sessions
		   SET ended_at = $2, duration_ms = $3, turns = $4, exit_code = $5
		 WHERE session_id = $1::uuid
	`, p.SessionID, p.EndedAt, p.DurationMS, p.Turns, p.ExitCode); err != nil {
		log.Printf("obs/sessions/finalize: %v", err)
		obsWrite(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	obsWrite(w, http.StatusAccepted, map[string]string{"status": "finalized"})
}

// ObsIngestEvents handles POST /observatory/api/v1/ingest/events.
// Fire-and-forget: bad rows are skipped with a log; partial success still returns 202.
func (r *Relay) ObsIngestEvents(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.NotFound(w, req)
		return
	}
	if !obsCheckPool(r, w) {
		return
	}
	var batch ingestBatchIn
	if err := json.NewDecoder(req.Body).Decode(&batch); err != nil {
		obsWrite(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}

	ctx := req.Context()
	acceptedEvents, acceptedDeltas, flagsFired := 0, 0, 0

	for _, ev := range batch.Events {
		n, err := obsInsertEvent(ctx, r.ObservatoryDB, ev)
		if err != nil {
			log.Printf("obs/events: persist failed session=%s: %v", ev.SessionID, err)
			continue
		}
		acceptedEvents++
		flagsFired += n
	}
	for _, d := range batch.TokenDeltas {
		if err := obsInsertTokenDelta(ctx, r.ObservatoryDB, d); err != nil {
			log.Printf("obs/events: token_delta failed session=%s: %v", d.SessionID, err)
			continue
		}
		acceptedDeltas++
	}
	obsWrite(w, http.StatusAccepted, map[string]any{
		"accepted_events":       acceptedEvents,
		"accepted_token_deltas": acceptedDeltas,
		"flags_fired":           flagsFired,
	})
}

// obsInsertEvent scrubs, persists one event row, and writes any fired secret-rule
// flags. Returns the number of distinct rules that fired.
func obsInsertEvent(ctx context.Context, pool *pgxpool.Pool, ev obsEventIn) (int, error) {
	// Scrub each side independently; collect flags before replacement so the
	// original matched text is captured in the flag record, not [REDACTED:...].
	var inFlags, outFlags []FlagRecord
	scrubbedIn := ev.Input
	scrubbedOut := ev.Output
	if ev.Input != nil {
		scrubbedIn, inFlags = ScrubPayload(ev.Input)
	}
	if ev.Output != nil {
		scrubbedOut, outFlags = ScrubPayload(ev.Output)
	}

	// Deduplicate flags by rule_id across both sides (first occurrence wins).
	firedRules := make(map[string]string)
	for _, f := range inFlags {
		if _, seen := firedRules[f.RuleID]; !seen {
			firedRules[f.RuleID] = obsSeverityFor(f.RuleID)
		}
	}
	for _, f := range outFlags {
		if _, seen := firedRules[f.RuleID]; !seen {
			firedRules[f.RuleID] = obsSeverityFor(f.RuleID)
		}
	}

	var eventID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO events
		    (session_id, task_run_id, trace_id, ts, kind, tool, path, input, output, turn_index)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7,
		        $8::jsonb, $9::jsonb, $10)
		RETURNING event_id
	`, ev.SessionID, ev.TaskRunID, ev.TraceID, ev.TS, ev.Kind,
		ev.Tool, ev.Path, obsJSONB(scrubbedIn), obsJSONB(scrubbedOut), ev.TurnIndex,
	).Scan(&eventID); err != nil {
		return 0, err
	}

	for ruleID, severity := range firedRules {
		if _, err := pool.Exec(ctx, `
			INSERT INTO flags (event_id, session_id, rule_id, severity, captured_at)
			VALUES ($1, $2::uuid, $3, $4, $5)
		`, eventID, ev.SessionID, ruleID, severity, ev.TS); err != nil {
			log.Printf("obs/events: flag insert rule=%s: %v", ruleID, err)
		}
	}
	return len(firedRules), nil
}

// obsInsertTokenDelta persists one token-usage record and computes cost_usd
// from the pricing table. cost_usd is left NULL when no pricing row covers
// (model, ts) — a follow-up backfill can populate it once the row is seeded.
func obsInsertTokenDelta(ctx context.Context, pool *pgxpool.Pool, d obsTokenDeltaIn) error {
	var costUSD *float64
	var inP, outP, cacheRP, cache5mP, cache1hP float64
	priceErr := pool.QueryRow(ctx, `
		SELECT input_per_mtok, output_per_mtok, cache_read_per_mtok,
		       cache_write_5m_per_mtok, cache_write_1h_per_mtok
		  FROM pricing
		 WHERE model = $1
		   AND effective_from <= $2
		   AND (effective_to IS NULL OR effective_to > $2)
		 ORDER BY effective_from DESC
		 LIMIT 1
	`, d.Model, d.TS).Scan(&inP, &outP, &cacheRP, &cache5mP, &cache1hP)
	if priceErr != nil && !errors.Is(priceErr, pgx.ErrNoRows) {
		return priceErr
	}
	if priceErr == nil {
		cost := (float64(d.InputTokens)*inP +
			float64(d.OutputTokens)*outP +
			float64(d.CacheReadInputTokens)*cacheRP +
			float64(d.CacheCreationInputTokens5m)*cache5mP +
			float64(d.CacheCreationInputTokens1h)*cache1hP) / 1_000_000.0
		costUSD = &cost
	}

	// ux_token_deltas_session_msg partial unique index deduplicates re-emits
	// from a fixed runner (same message_id for the same session). NULL message_id
	// rows are excluded from the index and insert normally.
	_, err := pool.Exec(ctx, `
		INSERT INTO token_deltas
		    (session_id, turn_index, message_id, model, ts,
		     input_tokens, output_tokens, cache_read_input_tokens,
		     cache_creation_input_tokens_5m, cache_creation_input_tokens_1h,
		     cost_usd)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (session_id, message_id) WHERE message_id IS NOT NULL DO NOTHING
	`, d.SessionID, d.TurnIndex, d.MessageID, d.Model, d.TS,
		d.InputTokens, d.OutputTokens, d.CacheReadInputTokens,
		d.CacheCreationInputTokens5m, d.CacheCreationInputTokens1h,
		costUSD)
	return err
}

// ObsIngestEstimates handles POST /observatory/api/v1/ingest/estimates.
// Upserts on (task_id, estimator_source) so re-emits are idempotent.
func (r *Relay) ObsIngestEstimates(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.NotFound(w, req)
		return
	}
	if !obsCheckPool(r, w) {
		return
	}
	var p obsTaskEstimateIn
	if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
		obsWrite(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	if _, err := r.ObservatoryDB.Exec(req.Context(), `
		INSERT INTO task_estimates
		    (task_id, estimator_source, estimated_at, estimator_agent_id, estimator_model,
		     complexity, est_tokens_input, est_tokens_output, est_duration_s, est_files,
		     est_risk_flags, rationale)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12)
		ON CONFLICT (task_id, estimator_source) DO UPDATE
		   SET estimated_at       = EXCLUDED.estimated_at,
		       estimator_agent_id = EXCLUDED.estimator_agent_id,
		       estimator_model    = EXCLUDED.estimator_model,
		       complexity         = EXCLUDED.complexity,
		       est_tokens_input   = EXCLUDED.est_tokens_input,
		       est_tokens_output  = EXCLUDED.est_tokens_output,
		       est_duration_s     = EXCLUDED.est_duration_s,
		       est_files          = EXCLUDED.est_files,
		       est_risk_flags     = EXCLUDED.est_risk_flags,
		       rationale          = EXCLUDED.rationale
	`, p.TaskID, p.EstimatorSource, p.EstimatedAt, p.EstimatorAgentID, p.EstimatorModel,
		p.Complexity, p.EstTokensInput, p.EstTokensOutput, p.EstDurationS, p.EstFiles,
		obsJSONB(p.EstRiskFlags), p.Rationale); err != nil {
		log.Printf("obs/estimates: %v", err)
		obsWrite(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	obsWrite(w, http.StatusAccepted, map[string]string{"status": "stored"})
}

// ObsIngestTaskRuns handles POST /observatory/api/v1/ingest/task_runs.
// FK violations (session_id not yet registered) are dropped silently per spec:
// the runner emitter is fire-and-forget and a missing session is a common transient.
func (r *Relay) ObsIngestTaskRuns(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.NotFound(w, req)
		return
	}
	if !obsCheckPool(r, w) {
		return
	}
	var p taskRunUpsertIn
	if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
		obsWrite(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	_, err := r.ObservatoryDB.Exec(req.Context(), `
		INSERT INTO task_runs
		    (task_run_id, task_id, session_id, claimed_at, started_at,
		     completed_at, terminal_state, outcome_summary)
		VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6, $7, $8)
		ON CONFLICT (task_run_id) DO UPDATE
		   SET claimed_at      = COALESCE(EXCLUDED.claimed_at,      task_runs.claimed_at),
		       started_at      = COALESCE(EXCLUDED.started_at,      task_runs.started_at),
		       completed_at    = COALESCE(EXCLUDED.completed_at,    task_runs.completed_at),
		       terminal_state  = COALESCE(EXCLUDED.terminal_state,  task_runs.terminal_state),
		       outcome_summary = COALESCE(EXCLUDED.outcome_summary, task_runs.outcome_summary)
	`, p.TaskRunID, p.TaskID, p.SessionID, p.ClaimedAt, p.StartedAt, p.CompletedAt, p.TerminalState, p.OutcomeSummary)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			log.Printf("obs/task_runs: dropped fk violation task_id=%s session=%s", p.TaskID, p.SessionID)
			obsWrite(w, http.StatusAccepted, map[string]string{"status": "dropped"})
			return
		}
		log.Printf("obs/task_runs: %v", err)
		obsWrite(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	obsWrite(w, http.StatusAccepted, map[string]string{"status": "stored"})
}
