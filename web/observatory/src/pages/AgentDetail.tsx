import { Link, useParams } from "react-router-dom";

import { StatCard } from "../components/StatCard";
import { formatCost, formatDateTime, formatDurationMs } from "../lib/format";
import { useApi } from "../lib/api";
import type { Agent, SessionListRow } from "../lib/api";

export default function AgentDetail() {
  const { slug = "" } = useParams();

  const agent = useApi<Agent>(slug ? `/agents/${encodeURIComponent(slug)}` : null);
  // Sessions are keyed by the resolved agent_id, so the fetch waits for the
  // agent lookup to land.
  const sessions = useApi<SessionListRow[]>(
    agent.data ? `/agents/${encodeURIComponent(agent.data.agent_id)}/sessions?limit=50` : null,
  );

  if (agent.error) {
    return (
      <>
        <header className="page-header">
          <h2>{slug}</h2>
        </header>
        <div className="error">Agent not found: {agent.error}</div>
      </>
    );
  }

  const a = agent.data;
  const sessionRows = sessions.data ?? [];
  const totalCost = sessionRows.reduce((sum, s) => sum + s.total_cost_usd, 0);
  const totalFlags = sessionRows.reduce((sum, s) => sum + s.flag_count, 0);

  return (
    <>
      <header className="page-header">
        <h2>{a?.profile_slug ?? slug}</h2>
        <div className="subtitle">
          <code>{a?.agent_id ?? "…"}</code>
          {a?.role ? ` · ${a.role}` : ""}
          {a?.project_slug ? (
            <>
              {" · "}
              <Link to={`/projects/${a.project_slug}`}>{a.project_slug}</Link>
            </>
          ) : null}
        </div>
      </header>

      <div className="cards">
        <StatCard label="Sessions 7d" value={a?.session_count_7d ?? "—"} />
        <StatCard label="Last session" value={formatDateTime(a?.last_session_at)} />
        <StatCard label="Cost (last 50)" value={formatCost(totalCost)} />
        <StatCard label="Flags (last 50)" value={totalFlags} />
        <StatCard label="Model" value={<code>{a?.model ?? "—"}</code>} />
      </div>

      <section className="section">
        <h3>Recent sessions</h3>
        {sessions.loading ? (
          <div className="loading">Loading…</div>
        ) : sessionRows.length === 0 ? (
          <div className="empty">No sessions recorded.</div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Started</th>
                <th>Duration</th>
                <th>Turns</th>
                <th>Model</th>
                <th className="num">Cost</th>
                <th className="num">Flags</th>
                <th>Exit</th>
                <th>Session</th>
              </tr>
            </thead>
            <tbody>
              {sessionRows.map((s) => (
                <tr key={s.session_id}>
                  <td>{formatDateTime(s.started_at)}</td>
                  <td>{formatDurationMs(s.duration_ms)}</td>
                  <td className="num">{s.turns ?? "—"}</td>
                  <td>
                    <code>{s.model}</code>
                  </td>
                  <td className="num">{formatCost(s.total_cost_usd)}</td>
                  <td className="num">{s.flag_count}</td>
                  <td>{s.exit_code === null ? "—" : s.exit_code}</td>
                  <td>
                    <Link to={`/sessions/${s.session_id}`}>{s.session_id.slice(0, 8)}…</Link>
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
