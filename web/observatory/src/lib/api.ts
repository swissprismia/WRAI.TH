import { useEffect, useState } from "react";

// All observatory read endpoints live under this prefix, served by the relay
// (internal/relay/observatory_read_handlers.go + observatory_read_aggregates.go)
// behind EasyAuth. The SPA itself is served at /observatory/.
const BASE = "/observatory/api/v1";

export async function apiGet<T>(path: string, signal?: AbortSignal): Promise<T> {
  const resp = await fetch(`${BASE}${path}`, { cache: "no-store", signal });
  if (!resp.ok) {
    const body = await resp.text().catch(() => "");
    throw new Error(`HTTP ${resp.status}${body ? `: ${body.slice(0, 200)}` : ""}`);
  }
  return (await resp.json()) as T;
}

export interface AsyncState<T> {
  data: T | null;
  error: string | null;
  loading: boolean;
}

// useApi fetches `path` (relative to BASE) and re-fetches whenever it changes.
// Pass null to skip the fetch (e.g. while a route param is unresolved).
export function useApi<T>(path: string | null): AsyncState<T> {
  const [state, setState] = useState<AsyncState<T>>({
    data: null,
    error: null,
    loading: path !== null,
  });

  useEffect(() => {
    if (path === null) {
      setState({ data: null, error: null, loading: false });
      return;
    }
    const ctrl = new AbortController();
    setState((s) => ({ ...s, loading: true, error: null }));
    apiGet<T>(path, ctrl.signal)
      .then((data) => setState({ data, error: null, loading: false }))
      .catch((err: unknown) => {
        if (ctrl.signal.aborted) return;
        setState({ data: null, error: (err as Error).message, loading: false });
      });
    return () => ctrl.abort();
  }, [path]);

  return state;
}

// ─── wire types (mirror the relay JSON: snake_case, numbers as numbers) ──────

export interface Overview {
  today_cost_usd: number;
  today_input_tokens: number;
  today_output_tokens: number;
  active_agents: number;
  tasks_in_flight: number;
  forecast_accuracy_7d: number | null;
}

export interface BurnRow {
  day_bucket: string;
  project_slug: string | null;
  model: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_input_tokens: number;
  cache_creation_5m: number;
  cache_creation_1h: number;
  cost_usd: number;
}

export interface Project {
  slug: string;
  github_repo: string | null;
  vault_share: string | null;
  agent_count: number;
  task_count_7d: number;
}

export interface Agent {
  agent_id: string;
  profile_slug: string;
  project_slug: string | null;
  role: string | null;
  model: string | null;
  created_at: string;
  last_session_at: string | null;
  session_count_7d: number;
}

export interface SessionListRow {
  session_id: string;
  worker_run_id: string;
  trace_id: string | null;
  spawn_index: number;
  model: string;
  started_at: string;
  ended_at: string | null;
  duration_ms: number | null;
  turns: number | null;
  exit_code: number | null;
  total_cost_usd: number;
  flag_count: number;
}

export interface SessionDetail extends SessionListRow {
  agent_id: string | null;
  profile_slug: string | null;
  project_slug: string | null;
  mode: string | null;
}

export interface EventRow {
  event_id: number;
  session_id: string;
  task_run_id: string | null;
  trace_id: string | null;
  ts: string;
  kind: string;
  tool: string | null;
  path: string | null;
  input: unknown;
  output: unknown;
  turn_index: number | null;
}

export interface TokenDeltaRow {
  id: number;
  session_id: string;
  turn_index: number | null;
  message_id: string | null;
  model: string;
  ts: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_input_tokens: number;
  cache_creation_input_tokens_5m: number;
  cache_creation_input_tokens_1h: number;
  cost_usd: number | null;
}

export interface FlagRow {
  flag_id: number;
  event_id: number;
  rule_id: string;
  severity: string;
  captured_at: string;
}

export interface TopFlagRow {
  rule_id: string;
  severity: string;
  count: number;
}

export interface Budget {
  profile_slug: string;
  agent_count: number;
  daily_input_tokens: number;
  daily_output_tokens: number;
  daily_cost_usd: number;
}

export interface TaskRun {
  task_run_id: string;
  task_id: string;
  session_id: string;
  claimed_at: string | null;
  started_at: string | null;
  completed_at: string | null;
  terminal_state: string | null;
  outcome_summary: string | null;
  forecast_accuracy: number | null;
}

export interface TaskEstimate {
  task_id: string;
  estimator_source: string;
  estimated_at: string;
  estimator_agent_id: string | null;
  estimator_model: string | null;
  complexity: string;
  est_tokens_input: number;
  est_tokens_output: number;
  est_duration_s: number;
  est_files: number;
  est_risk_flags: string[] | null;
  rationale: string | null;
}

export interface TaskActuals {
  task_id: string;
  total_input_tokens: number;
  total_output_tokens: number;
  total_duration_s: number;
  total_files_touched: number;
}

// WindowDays is the supported burn/flags window in days.
export type WindowDays = 1 | 7 | 30;

export function parseWindow(raw: string | null): WindowDays {
  const n = raw ? Number.parseInt(raw, 10) : 7;
  return n === 1 || n === 30 ? n : 7;
}
