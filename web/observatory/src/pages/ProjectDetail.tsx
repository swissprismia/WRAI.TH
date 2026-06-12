import { Link, useParams, useSearchParams } from "react-router-dom";

import { StatCard } from "../components/StatCard";
import { WindowPills } from "../components/WindowPills";
import { formatCost, formatDateTime, formatTokens } from "../lib/format";
import { parseWindow, useApi } from "../lib/api";
import type { Agent, BurnRow, Project } from "../lib/api";

export default function ProjectDetail() {
  const { slug = "" } = useParams();
  const [sp] = useSearchParams();
  const windowDays = parseWindow(sp.get("window"));

  const project = useApi<Project>(slug ? `/projects/${encodeURIComponent(slug)}` : null);
  const agents = useApi<Agent[]>(slug ? `/agents?project=${encodeURIComponent(slug)}` : null);
  const burn = useApi<BurnRow[]>(`/burn?window=${windowDays}`);

  if (project.error) {
    return (
      <>
        <header className="page-header">
          <h2>{slug}</h2>
        </header>
        <div className="error">Project not found: {project.error}</div>
      </>
    );
  }

  const p = project.data;
  const burnRows = (burn.data ?? []).filter((b) => b.project_slug === slug);
  const totalCost = burnRows.reduce((sum, b) => sum + b.cost_usd, 0);
  const totalInput = burnRows.reduce((sum, b) => sum + b.input_tokens, 0);
  const totalOutput = burnRows.reduce((sum, b) => sum + b.output_tokens, 0);
  const agentRows = agents.data ?? [];

  return (
    <>
      <header className="page-header">
        <h2>{p?.slug ?? slug}</h2>
        <div className="subtitle">
          {p?.github_repo ?? "(no repo registered)"}
          {p?.vault_share ? ` · vault ${p.vault_share}` : ""}
        </div>
      </header>

      <div className="cards">
        <StatCard label={`Cost (${windowDays}d)`} value={formatCost(totalCost)} />
        <StatCard label={`Input tokens (${windowDays}d)`} value={formatTokens(totalInput)} />
        <StatCard label={`Output tokens (${windowDays}d)`} value={formatTokens(totalOutput)} />
        <StatCard label="Agents" value={p?.agent_count ?? "—"} />
        <StatCard label="Tasks 7d" value={p?.task_count_7d ?? "—"} />
      </div>

      <WindowPills active={windowDays} />

      <section className="section">
        <h3>Daily burn</h3>
        {burn.loading ? (
          <div className="loading">Loading…</div>
        ) : burnRows.length === 0 ? (
          <div className="empty">No spend in this window.</div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Day</th>
                <th>Model</th>
                <th className="num">Input</th>
                <th className="num">Output</th>
                <th className="num">Cache read</th>
                <th className="num">Cost</th>
              </tr>
            </thead>
            <tbody>
              {burnRows.map((row, i) => (
                <tr key={`${row.day_bucket}-${row.model}-${i}`}>
                  <td>{formatDateTime(row.day_bucket)}</td>
                  <td>
                    <code>{row.model}</code>
                  </td>
                  <td className="num">{formatTokens(row.input_tokens)}</td>
                  <td className="num">{formatTokens(row.output_tokens)}</td>
                  <td className="num">{formatTokens(row.cache_read_input_tokens)}</td>
                  <td className="num">{formatCost(row.cost_usd)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <section className="section">
        <h3>Agents on this project</h3>
        {agents.loading ? (
          <div className="loading">Loading…</div>
        ) : agentRows.length === 0 ? (
          <div className="empty">No agents registered.</div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Profile</th>
                <th>Role</th>
                <th>Model</th>
                <th>Last session</th>
                <th className="num">Sessions 7d</th>
              </tr>
            </thead>
            <tbody>
              {agentRows.map((a) => (
                <tr key={a.agent_id}>
                  <td>
                    <Link to={`/agents/${a.profile_slug}`}>{a.profile_slug}</Link>
                  </td>
                  <td>{a.role ?? "—"}</td>
                  <td>
                    <code>{a.model ?? "—"}</code>
                  </td>
                  <td>{formatDateTime(a.last_session_at)}</td>
                  <td className="num">{a.session_count_7d}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </>
  );
}
