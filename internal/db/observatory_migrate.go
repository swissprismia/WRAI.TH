package db

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RunObservatoryMigrations applies versioned SQL migrations to the observatory
// Postgres database. It creates observatory_schema_version if it does not exist,
// then skips any revision that is already recorded there — so it is safe to call
// at every relay boot.
//
// pg_cron refresh wiring is intentionally omitted; it is tracked in OQ-4 and
// will land in a follow-up migration.
func RunObservatoryMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	// Bootstrap the observatory schema and set the search path for this session.
	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS observatory`); err != nil {
		return fmt.Errorf("observatory migrations: create schema: %w", err)
	}
	if _, err := pool.Exec(ctx, `SET search_path = observatory`); err != nil {
		return fmt.Errorf("observatory migrations: set search_path: %w", err)
	}

	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS observatory_schema_version (
			version    INTEGER     PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("observatory migrations: create version table: %w", err)
	}

	for _, rev := range observatoryRevisions {
		var applied bool
		if err := pool.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM observatory_schema_version WHERE version = $1)",
			rev.version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("observatory migrations: check version %d: %w", rev.version, err)
		}
		if applied {
			continue
		}

		log.Printf("observatory: applying revision %04d (%s)", rev.version, rev.name)

		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("observatory migrations: begin revision %d: %w", rev.version, err)
		}

		for _, stmt := range rev.stmts {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				_ = tx.Rollback(ctx)
				return fmt.Errorf("observatory migrations: revision %d (%s): %w", rev.version, rev.name, err)
			}
		}

		if _, err := tx.Exec(ctx,
			"INSERT INTO observatory_schema_version (version) VALUES ($1)",
			rev.version,
		); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("observatory migrations: record revision %d: %w", rev.version, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("observatory migrations: commit revision %d: %w", rev.version, err)
		}

		log.Printf("observatory: revision %04d applied", rev.version)
	}

	return nil
}

type observatoryRevision struct {
	version int
	name    string
	stmts   []string
}

// observatoryRevisions is the ordered list of schema revisions ported from
// agt-geonosis/sources/observatory/api/alembic/versions/ (0001–0005).
var observatoryRevisions = []observatoryRevision{
	{
		version: 1,
		name:    "initial_schema",
		stmts: []string{
			`CREATE TABLE projects (
				slug        TEXT PRIMARY KEY,
				github_repo TEXT,
				vault_share TEXT,
				created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`,

			`CREATE TABLE agents (
				agent_id     TEXT PRIMARY KEY,
				profile_slug TEXT NOT NULL,
				project_slug TEXT REFERENCES projects(slug) ON DELETE RESTRICT,
				role         TEXT,
				model        TEXT,
				created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`,
			`CREATE INDEX ix_agents_profile_slug ON agents(profile_slug)`,
			`CREATE INDEX ix_agents_project_slug ON agents(project_slug)`,

			`CREATE TABLE worker_runs (
				worker_run_id      UUID PRIMARY KEY,
				agent_id           TEXT REFERENCES agents(agent_id) ON DELETE RESTRICT,
				trace_id           UUID,
				started_at         TIMESTAMPTZ NOT NULL,
				ended_at           TIMESTAMPTZ,
				exit_code          INTEGER,
				mode               TEXT NOT NULL,
				container_revision TEXT,
				CONSTRAINT ck_worker_runs_mode CHECK (mode IN ('permanent', 'pool'))
			)`,
			`CREATE INDEX ix_worker_runs_started_at ON worker_runs(started_at)`,
			`CREATE INDEX ix_worker_runs_trace_id ON worker_runs(trace_id)`,

			`CREATE TABLE sessions (
				session_id    UUID PRIMARY KEY,
				worker_run_id UUID NOT NULL REFERENCES worker_runs(worker_run_id) ON DELETE CASCADE,
				trace_id      UUID,
				spawn_index   INTEGER NOT NULL DEFAULT 0,
				model         TEXT NOT NULL,
				started_at    TIMESTAMPTZ NOT NULL,
				ended_at      TIMESTAMPTZ,
				duration_ms   BIGINT,
				turns         INTEGER,
				exit_code     INTEGER
			)`,
			`CREATE INDEX ix_sessions_worker_run_id ON sessions(worker_run_id)`,
			`CREATE INDEX ix_sessions_started_at ON sessions(started_at)`,
			`CREATE INDEX ix_sessions_trace_id ON sessions(trace_id)`,

			`CREATE TABLE task_runs (
				task_run_id       UUID PRIMARY KEY,
				task_id           TEXT NOT NULL,
				session_id        UUID NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
				claimed_at        TIMESTAMPTZ,
				started_at        TIMESTAMPTZ,
				completed_at      TIMESTAMPTZ,
				terminal_state    TEXT,
				outcome_summary   TEXT,
				forecast_accuracy NUMERIC(5,4),
				CONSTRAINT ck_task_runs_terminal_state CHECK (
					terminal_state IS NULL OR terminal_state IN ('done','blocked','cancelled')
				)
			)`,
			`CREATE INDEX ix_task_runs_task_id ON task_runs(task_id)`,
			`CREATE INDEX ix_task_runs_session_id ON task_runs(session_id)`,

			`CREATE TABLE task_estimates (
				task_id            TEXT NOT NULL,
				estimator_source   TEXT NOT NULL,
				estimated_at       TIMESTAMPTZ NOT NULL,
				estimator_agent_id TEXT,
				estimator_model    TEXT,
				complexity         TEXT NOT NULL,
				est_tokens_input   INTEGER NOT NULL DEFAULT 0,
				est_tokens_output  INTEGER NOT NULL DEFAULT 0,
				est_duration_s     INTEGER NOT NULL DEFAULT 0,
				est_files          INTEGER NOT NULL DEFAULT 0,
				est_risk_flags     JSONB,
				rationale          TEXT,
				CONSTRAINT pk_task_estimates PRIMARY KEY (task_id, estimator_source),
				CONSTRAINT ck_task_estimates_source CHECK (
					estimator_source IN ('dispatcher_envelope','executor_first_turn')
				),
				CONSTRAINT ck_task_estimates_complexity CHECK (
					complexity IN ('S','M','L','XL')
				)
			)`,
			`CREATE INDEX ix_task_estimates_risk_flags ON task_estimates USING gin(est_risk_flags)`,

			`CREATE TABLE events (
				event_id    BIGSERIAL PRIMARY KEY,
				session_id  UUID NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
				task_run_id UUID REFERENCES task_runs(task_run_id) ON DELETE SET NULL,
				trace_id    UUID,
				ts          TIMESTAMPTZ NOT NULL,
				kind        TEXT NOT NULL,
				tool        TEXT,
				path        TEXT,
				input       JSONB,
				output      JSONB,
				turn_index  INTEGER
			)`,
			`CREATE INDEX ix_events_ts ON events(ts)`,
			`CREATE INDEX ix_events_session_ts ON events(session_id, ts)`,
			`CREATE INDEX ix_events_trace_id ON events(trace_id)`,
			`CREATE INDEX ix_events_kind ON events(kind)`,
			`CREATE INDEX ix_events_input_gin ON events USING gin(input)`,

			`CREATE TABLE token_deltas (
				id                             BIGSERIAL PRIMARY KEY,
				session_id                     UUID NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
				turn_index                     INTEGER,
				message_id                     TEXT,
				model                          TEXT NOT NULL,
				ts                             TIMESTAMPTZ NOT NULL,
				input_tokens                   INTEGER NOT NULL DEFAULT 0,
				output_tokens                  INTEGER NOT NULL DEFAULT 0,
				cache_read_input_tokens        INTEGER NOT NULL DEFAULT 0,
				cache_creation_input_tokens_5m INTEGER NOT NULL DEFAULT 0,
				cache_creation_input_tokens_1h INTEGER NOT NULL DEFAULT 0,
				cost_usd                       NUMERIC(14,6)
			)`,
			`CREATE INDEX ix_token_deltas_session_id ON token_deltas(session_id)`,
			`CREATE INDEX ix_token_deltas_ts ON token_deltas(ts)`,
			`CREATE INDEX ix_token_deltas_model_ts ON token_deltas(model, ts)`,

			`CREATE TABLE flags (
				flag_id     BIGSERIAL PRIMARY KEY,
				event_id    BIGINT NOT NULL REFERENCES events(event_id) ON DELETE CASCADE,
				session_id  UUID NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
				rule_id     TEXT NOT NULL,
				severity    TEXT NOT NULL,
				captured_at TIMESTAMPTZ NOT NULL,
				CONSTRAINT ck_flags_severity CHECK (
					severity IN ('critical','high','medium','low','info')
				)
			)`,
			`CREATE INDEX ix_flags_session_id ON flags(session_id)`,
			`CREATE INDEX ix_flags_rule_id ON flags(rule_id)`,
			`CREATE INDEX ix_flags_severity ON flags(severity)`,
			`CREATE INDEX ix_flags_captured_at ON flags(captured_at)`,

			`CREATE TABLE pricing (
				model                   TEXT NOT NULL,
				effective_from          TIMESTAMPTZ NOT NULL,
				effective_to            TIMESTAMPTZ,
				input_per_mtok          NUMERIC(12,4) NOT NULL,
				output_per_mtok         NUMERIC(12,4) NOT NULL,
				cache_read_per_mtok     NUMERIC(12,4) NOT NULL,
				cache_write_5m_per_mtok NUMERIC(12,4) NOT NULL,
				cache_write_1h_per_mtok NUMERIC(12,4) NOT NULL,
				CONSTRAINT pk_pricing PRIMARY KEY (model, effective_from)
			)`,

			// Seed: published Anthropic rates per ADF-026 §Cost Model.
			// The open-ended row (effective_to = NULL) is the active rate.
			// Cache-write rates are split by TTL tier (5m / 1h); the 1h tier
			// is priced 2× the 5m tier per published rates as of 2026-05-08.
			`INSERT INTO pricing
				(model, effective_from, effective_to,
				 input_per_mtok, output_per_mtok,
				 cache_read_per_mtok, cache_write_5m_per_mtok, cache_write_1h_per_mtok)
			VALUES
				('claude-opus-4-7',   '2026-01-01 00:00:00+00', NULL, 15.0000, 75.0000, 1.5000, 18.7500, 37.5000),
				('claude-sonnet-4-6', '2026-01-01 00:00:00+00', NULL,  3.0000, 15.0000, 0.3000,  3.7500,  7.5000),
				('claude-haiku-4-5',  '2026-01-01 00:00:00+00', NULL,  1.0000,  5.0000, 0.1000,  1.2500,  2.5000)`,

			// v_task_estimate picks the priority-winner estimate per task_id.
			// dispatcher_envelope > executor_first_turn per ADF-026 §Forecast accuracy.
			`CREATE VIEW v_task_estimate AS
			SELECT DISTINCT ON (task_id)
			       task_id,
			       estimator_source,
			       estimated_at,
			       estimator_agent_id,
			       estimator_model,
			       complexity,
			       est_tokens_input,
			       est_tokens_output,
			       est_duration_s,
			       est_files,
			       est_risk_flags,
			       rationale
			  FROM task_estimates
			 ORDER BY task_id,
			          CASE estimator_source
			               WHEN 'dispatcher_envelope'  THEN 0
			               WHEN 'executor_first_turn'  THEN 1
			               ELSE 9
			          END,
			          estimated_at DESC`,
		},
	},

	{
		version: 2,
		name:    "materialized_views",
		// pg_cron refresh scheduling is intentionally omitted (tracked in OQ-4).
		// The views are created WITH NO DATA and bootstrapped by revision 0003.
		stmts: []string{
			`CREATE MATERIALIZED VIEW mv_daily_burn_by_project AS
			SELECT
			    COALESCE(a.project_slug, '<unattributed>') AS project_slug,
			    date_trunc('day', td.ts) AT TIME ZONE 'UTC'  AS day_bucket,
			    td.model,
			    SUM(td.input_tokens)::bigint                    AS input_tokens,
			    SUM(td.output_tokens)::bigint                   AS output_tokens,
			    SUM(td.cache_read_input_tokens)::bigint         AS cache_read_input_tokens,
			    SUM(td.cache_creation_input_tokens_5m)::bigint  AS cache_creation_5m,
			    SUM(td.cache_creation_input_tokens_1h)::bigint  AS cache_creation_1h,
			    SUM(COALESCE(td.cost_usd, 0))::numeric(14,6)   AS cost_usd
			  FROM token_deltas td
			  JOIN sessions    s  ON s.session_id     = td.session_id
			  JOIN worker_runs wr ON wr.worker_run_id = s.worker_run_id
			  LEFT JOIN agents a  ON a.agent_id       = wr.agent_id
			 GROUP BY 1, 2, 3
			WITH NO DATA`,
			`CREATE UNIQUE INDEX ux_mv_daily_burn
			    ON mv_daily_burn_by_project (project_slug, day_bucket, model)`,

			`CREATE MATERIALIZED VIEW mv_session_aggregates AS
			SELECT
			    s.session_id,
			    s.worker_run_id,
			    s.model,
			    s.started_at,
			    s.ended_at,
			    s.duration_ms,
			    s.turns,
			    COALESCE(td.tot_input,  0)::bigint         AS total_input_tokens,
			    COALESCE(td.tot_output, 0)::bigint         AS total_output_tokens,
			    COALESCE(td.tot_cache_read, 0)::bigint     AS total_cache_read,
			    COALESCE(td.tot_cache_5m,  0)::bigint      AS total_cache_5m,
			    COALESCE(td.tot_cache_1h,  0)::bigint      AS total_cache_1h,
			    COALESCE(td.tot_cost, 0)::numeric(14,6)    AS total_cost_usd,
			    COALESCE(ev.tool_calls,  0)::bigint        AS tool_calls,
			    COALESCE(ev.tool_errors, 0)::bigint        AS tool_errors,
			    COALESCE(fl.flag_count,  0)::bigint        AS flag_count,
			    COALESCE(fl.critical_count, 0)::bigint     AS critical_flag_count
			  FROM sessions s
			  LEFT JOIN (
			      SELECT session_id,
			             SUM(input_tokens)                     AS tot_input,
			             SUM(output_tokens)                    AS tot_output,
			             SUM(cache_read_input_tokens)          AS tot_cache_read,
			             SUM(cache_creation_input_tokens_5m)   AS tot_cache_5m,
			             SUM(cache_creation_input_tokens_1h)   AS tot_cache_1h,
			             SUM(COALESCE(cost_usd, 0))            AS tot_cost
			        FROM token_deltas
			       GROUP BY session_id
			  ) td ON td.session_id = s.session_id
			  LEFT JOIN (
			      SELECT session_id,
			             SUM(CASE WHEN kind = 'tool_use' THEN 1 ELSE 0 END) AS tool_calls,
			             SUM(CASE WHEN kind = 'tool_result'
			                      AND output IS NOT NULL
			                      AND (output #>> '{is_error}') = 'true'
			                 THEN 1 ELSE 0 END) AS tool_errors
			        FROM events
			       GROUP BY session_id
			  ) ev ON ev.session_id = s.session_id
			  LEFT JOIN (
			      SELECT session_id,
			             COUNT(*) AS flag_count,
			             SUM(CASE WHEN severity = 'critical' THEN 1 ELSE 0 END) AS critical_count
			        FROM flags
			       GROUP BY session_id
			  ) fl ON fl.session_id = s.session_id
			WITH NO DATA`,
			`CREATE UNIQUE INDEX ux_mv_session_aggregates
			    ON mv_session_aggregates (session_id)`,

			// mv_estimate_accuracy joins the priority-winner estimate from
			// v_task_estimate to actual task_runs/token_deltas outcomes.
			// accuracy_d = 1 - min(|actual - estimate| / max(actual, estimate, eps), 1)
			// composite  = arithmetic mean of the three per-dimension accuracies.
			`CREATE MATERIALIZED VIEW mv_estimate_accuracy AS
			WITH actuals AS (
			    SELECT
			        tr.task_id,
			        tr.task_run_id,
			        tr.session_id,
			        tr.terminal_state,
			        COALESCE(EXTRACT(EPOCH FROM (tr.completed_at - tr.claimed_at))::int, 0)
			            AS actual_duration_s,
			        COALESCE((
			            SELECT SUM(input_tokens) + SUM(output_tokens)
			              FROM token_deltas
			             WHERE session_id = tr.session_id
			        ), 0)::bigint AS actual_tokens_total,
			        COALESCE((
			            SELECT COUNT(DISTINCT path)
			              FROM events
			             WHERE session_id = tr.session_id
			               AND kind = 'tool_use'
			               AND tool IN ('Edit','Write','MultiEdit','NotebookEdit')
			               AND path IS NOT NULL
			        ), 0)::bigint AS actual_files
			      FROM task_runs tr
			), joined AS (
			    SELECT
			        e.task_id,
			        e.estimator_source,
			        e.complexity,
			        (e.est_tokens_input + e.est_tokens_output)::bigint AS est_tokens_total,
			        e.est_duration_s,
			        e.est_files,
			        a.task_run_id,
			        a.session_id,
			        a.terminal_state,
			        a.actual_duration_s,
			        a.actual_tokens_total,
			        a.actual_files
			      FROM v_task_estimate e
			      JOIN actuals a ON a.task_id = e.task_id
			)
			SELECT
			    task_id,
			    estimator_source,
			    complexity,
			    est_tokens_total,
			    est_duration_s,
			    est_files,
			    actual_tokens_total,
			    actual_duration_s,
			    actual_files,
			    terminal_state,
			    (1.0 - LEAST(
			        ABS(actual_tokens_total - est_tokens_total)::float
			        / GREATEST(actual_tokens_total::float, est_tokens_total::float, 100.0),
			        1.0
			    ))::numeric(5,4) AS accuracy_tokens,
			    (1.0 - LEAST(
			        ABS(actual_duration_s - est_duration_s)::float
			        / GREATEST(actual_duration_s::float, est_duration_s::float, 5.0),
			        1.0
			    ))::numeric(5,4) AS accuracy_duration,
			    (1.0 - LEAST(
			        ABS(actual_files - est_files)::float
			        / GREATEST(actual_files::float, est_files::float, 1.0),
			        1.0
			    ))::numeric(5,4) AS accuracy_files,
			    (((1.0 - LEAST(
			        ABS(actual_tokens_total - est_tokens_total)::float
			        / GREATEST(actual_tokens_total::float, est_tokens_total::float, 100.0), 1.0))
			      + (1.0 - LEAST(
			        ABS(actual_duration_s - est_duration_s)::float
			        / GREATEST(actual_duration_s::float, est_duration_s::float, 5.0), 1.0))
			      + (1.0 - LEAST(
			        ABS(actual_files - est_files)::float
			        / GREATEST(actual_files::float, est_files::float, 1.0), 1.0))
			      ) / 3.0)::numeric(5,4) AS forecast_accuracy
			  FROM joined
			WITH NO DATA`,
			`CREATE UNIQUE INDEX ux_mv_estimate_accuracy
			    ON mv_estimate_accuracy (task_id, estimator_source)`,
		},
	},

	{
		version: 3,
		name:    "bootstrap_mv_refresh",
		// Populates the WITH NO DATA views from revision 0002 so that future
		// CONCURRENTLY refreshes (scheduled by OQ-4) do not fail on empty views.
		stmts: []string{
			`REFRESH MATERIALIZED VIEW mv_daily_burn_by_project`,
			`REFRESH MATERIALIZED VIEW mv_session_aggregates`,
			`REFRESH MATERIALIZED VIEW mv_estimate_accuracy`,
		},
	},

	{
		version: 4,
		name:    "session_agent_link",
		// Adds sessions.agent_id so project rollups attribute to the per-task
		// persona rather than the pool-worker container identity (which has no
		// project_slug and bucketed everything under <unattributed>).
		stmts: []string{
			`ALTER TABLE sessions
			    ADD COLUMN agent_id TEXT REFERENCES agents(agent_id) ON DELETE SET NULL`,
			`CREATE INDEX ix_sessions_agent_id ON sessions(agent_id)`,

			// Rebuild mv_daily_burn_by_project to join via sessions.agent_id
			// instead of the worker_run → agent path.
			`DROP MATERIALIZED VIEW mv_daily_burn_by_project`,
			`CREATE MATERIALIZED VIEW mv_daily_burn_by_project AS
			SELECT
			    COALESCE(a.project_slug, '<unattributed>') AS project_slug,
			    date_trunc('day', td.ts) AT TIME ZONE 'UTC'  AS day_bucket,
			    td.model,
			    SUM(td.input_tokens)::bigint                    AS input_tokens,
			    SUM(td.output_tokens)::bigint                   AS output_tokens,
			    SUM(td.cache_read_input_tokens)::bigint         AS cache_read_input_tokens,
			    SUM(td.cache_creation_input_tokens_5m)::bigint  AS cache_creation_5m,
			    SUM(td.cache_creation_input_tokens_1h)::bigint  AS cache_creation_1h,
			    SUM(COALESCE(td.cost_usd, 0))::numeric(14,6)   AS cost_usd
			  FROM token_deltas td
			  JOIN sessions    s  ON s.session_id = td.session_id
			  LEFT JOIN agents a  ON a.agent_id   = s.agent_id
			 GROUP BY 1, 2, 3
			WITH NO DATA`,
			`CREATE UNIQUE INDEX ux_mv_daily_burn
			    ON mv_daily_burn_by_project (project_slug, day_bucket, model)`,
			// Re-bootstrap so any existing pg_cron CONCURRENTLY refresh can resume.
			`REFRESH MATERIALIZED VIEW mv_daily_burn_by_project`,
		},
	},

	{
		version: 5,
		name:    "token_deltas_dedup",
		// Defense-in-depth against token_delta duplicates caused by the
		// adf-agent-runner bug fixed in 0.7.3. Keeps the lowest id per
		// (session_id, message_id); NULL message_id rows are untouched.
		stmts: []string{
			`DELETE FROM token_deltas
			  WHERE id IN (
			      SELECT id FROM (
			          SELECT id,
			                 ROW_NUMBER() OVER (
			                     PARTITION BY session_id, message_id
			                     ORDER BY id
			                 ) AS rn
			            FROM token_deltas
			           WHERE message_id IS NOT NULL
			      ) t
			      WHERE rn > 1
			  )`,

			`CREATE UNIQUE INDEX ux_token_deltas_session_msg
			    ON token_deltas (session_id, message_id)
			    WHERE message_id IS NOT NULL`,

			// Refresh so dashboards see the deduped totals immediately.
			`REFRESH MATERIALIZED VIEW mv_daily_burn_by_project`,
		},
	},
}
