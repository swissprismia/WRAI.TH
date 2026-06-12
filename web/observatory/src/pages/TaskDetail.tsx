import { Link, useParams } from "react-router-dom";

import { StatCard } from "../components/StatCard";
import { formatDateTime, formatDuration, formatPercent, formatTokens } from "../lib/format";
import { useApi } from "../lib/api";
import type { TaskActuals, TaskEstimate, TaskRun } from "../lib/api";

export default function TaskDetail() {
  const { id = "" } = useParams();
  const enc = encodeURIComponent(id);

  const estimates = useApi<TaskEstimate[]>(id ? `/tasks/${enc}/estimates` : null);
  const runs = useApi<TaskRun[]>(id ? `/tasks/${enc}/runs` : null);
  const actuals = useApi<TaskActuals>(id ? `/tasks/${enc}/actuals` : null);

  const estimateRows = estimates.data ?? [];
  const runRows = runs.data ?? [];
  const loading = estimates.loading || runs.loading;

  if (!loading && estimateRows.length === 0 && runRows.length === 0) {
    return (
      <>
        <header className="page-header">
          <h2>Task</h2>
          <div className="subtitle">
            <code>{id}</code>
          </div>
        </header>
        <div className="empty">No estimates or runs recorded for this task.</div>
      </>
    );
  }

  const winner = estimateRows[0];
  const latestRun = runRows[0];
  const act = actuals.data;
  const actualTokensTotal = act ? act.total_input_tokens + act.total_output_tokens : 0;
  const actualDuration = act ? act.total_duration_s : 0;
  const actualFiles = act ? act.total_files_touched : 0;

  return (
    <>
      <header className="page-header">
        <h2>Task</h2>
        <div className="subtitle">
          <code>{id}</code>
          {latestRun?.terminal_state ? ` · ${latestRun.terminal_state}` : ""}
        </div>
      </header>

      <div className="cards">
        <StatCard
          label="Forecast accuracy"
          value={latestRun?.forecast_accuracy != null ? formatPercent(latestRun.forecast_accuracy) : "—"}
        />
        <StatCard label="Sessions" value={runRows.length} />
        <StatCard label="Estimator winner" value={winner?.estimator_source ?? "—"} />
        <StatCard label="Complexity" value={winner?.complexity ?? "—"} />
      </div>

      {estimateRows.length > 0 ? (
        <section className="section">
          <h3>Estimates vs actuals</h3>
          <table>
            <thead>
              <tr>
                <th>Source</th>
                <th>Complexity</th>
                <th className="num">Est tokens (in+out)</th>
                <th className="num">Est duration</th>
                <th className="num">Est files</th>
                <th>Risks</th>
                <th>By</th>
              </tr>
            </thead>
            <tbody>
              {estimateRows.map((e) => (
                <tr key={e.estimator_source}>
                  <td>
                    <span
                      style={{ fontWeight: e.estimator_source === winner?.estimator_source ? 600 : 400 }}
                    >
                      {e.estimator_source}
                    </span>
                  </td>
                  <td>{e.complexity}</td>
                  <td className="num">{formatTokens(e.est_tokens_input + e.est_tokens_output)}</td>
                  <td className="num">{formatDuration(e.est_duration_s)}</td>
                  <td className="num">{e.est_files}</td>
                  <td style={{ fontSize: "0.8rem" }}>{(e.est_risk_flags ?? []).join(", ") || "—"}</td>
                  <td>
                    <code style={{ fontSize: "0.75rem" }}>{e.estimator_agent_id ?? "—"}</code>
                  </td>
                </tr>
              ))}
              {act ? (
                <tr style={{ background: "rgba(88, 166, 255, 0.06)" }}>
                  <td>
                    <strong>actual</strong>
                  </td>
                  <td>—</td>
                  <td className="num">{formatTokens(actualTokensTotal)}</td>
                  <td className="num">{formatDuration(actualDuration)}</td>
                  <td className="num">{actualFiles}</td>
                  <td>—</td>
                  <td>—</td>
                </tr>
              ) : null}
            </tbody>
          </table>
          {winner?.rationale ? (
            <div style={{ marginTop: "0.75rem", color: "var(--text-dim)", fontSize: "0.85rem" }}>
              <strong>Rationale ({winner.estimator_source}):</strong> {winner.rationale}
            </div>
          ) : null}
        </section>
      ) : null}

      <section className="section">
        <h3>Task runs</h3>
        {runs.loading ? (
          <div className="loading">Loading…</div>
        ) : runRows.length === 0 ? (
          <div className="empty">No runs recorded for this task.</div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Claimed</th>
                <th>Started</th>
                <th>Completed</th>
                <th>State</th>
                <th>Forecast acc.</th>
                <th>Session</th>
              </tr>
            </thead>
            <tbody>
              {runRows.map((r) => (
                <tr key={r.task_run_id}>
                  <td>{formatDateTime(r.claimed_at)}</td>
                  <td>{formatDateTime(r.started_at)}</td>
                  <td>{formatDateTime(r.completed_at)}</td>
                  <td>{r.terminal_state ?? "—"}</td>
                  <td>{r.forecast_accuracy != null ? formatPercent(r.forecast_accuracy) : "—"}</td>
                  <td>
                    <Link to={`/sessions/${r.session_id}`}>{r.session_id.slice(0, 8)}…</Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </>
  );
}
