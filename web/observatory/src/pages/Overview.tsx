import { Link, useSearchParams } from "react-router-dom";

import { SeverityBadge } from "../components/SeverityBadge";
import { StatCard } from "../components/StatCard";
import { WindowPills } from "../components/WindowPills";
import { formatCost, formatPercent, formatTokens } from "../lib/format";
import { parseWindow, useApi } from "../lib/api";
import type { BurnRow, Overview as OverviewT, Project, TopFlagRow } from "../lib/api";

interface BurnAgg {
  project_slug: string | null;
  input: number;
  output: number;
  cache_read: number;
  cost: number;
}

function aggregateBurnByProject(burn: BurnRow[]): BurnAgg[] {
  const acc = new Map<string, BurnAgg>();
  for (const row of burn) {
    const key = row.project_slug ?? "__unattached__";
    const existing =
      acc.get(key) ??
      { project_slug: row.project_slug, input: 0, output: 0, cache_read: 0, cost: 0 };
    existing.input += row.input_tokens;
    existing.output += row.output_tokens;
    existing.cache_read += row.cache_read_input_tokens;
    existing.cost += row.cost_usd;
    acc.set(key, existing);
  }
  return [...acc.values()].sort((a, b) => b.cost - a.cost);
}

export default function Overview() {
  const [sp] = useSearchParams();
  const windowDays = parseWindow(sp.get("window"));

  const overview = useApi<OverviewT>("/overview");
  const burn = useApi<BurnRow[]>(`/burn?window=${windowDays}`);
  const projects = useApi<Project[]>("/projects");
  const flags = useApi<TopFlagRow[]>(`/flags/top?window=${windowDays}`);

  const err = overview.error ?? burn.error ?? projects.error ?? flags.error;
  if (err) {
    return (
      <>
        <header className="page-header">
          <h2>Factory Overview</h2>
        </header>
        <div className="error">Database unavailable: {err}</div>
      </>
    );
  }

  const burnRows = burn.data ?? [];
  const burnByProject = aggregateBurnByProject(burnRows);
  const totalCost = burnRows.reduce((sum, b) => sum + b.cost_usd, 0);
  const o = overview.data;

  return (
    <>
      <header className="page-header">
        <h2>Factory Overview</h2>
        <div className="subtitle">Live cost, agents, and tasks across the dev-factory</div>
      </header>

      <div className="cards">
        <StatCard label="Today (cost)" value={formatCost(o?.today_cost_usd)} />
        <StatCard
          label="Today (tokens)"
          value={formatTokens((o?.today_input_tokens ?? 0) + (o?.today_output_tokens ?? 0))}
          delta={`${formatTokens(o?.today_input_tokens)} in · ${formatTokens(o?.today_output_tokens)} out`}
        />
        <StatCard label="Active agents" value={o?.active_agents ?? "—"} />
        <StatCard label="Tasks in flight" value={o?.tasks_in_flight ?? "—"} />
        <StatCard
          label="7d forecast accuracy"
          value={o?.forecast_accuracy_7d != null ? formatPercent(o.forecast_accuracy_7d) : "—"}
        />
      </div>

      <WindowPills active={windowDays} />

      <section className="section">
        <h3>
          Burn by project ({windowDays}d · total {formatCost(totalCost)})
        </h3>
        {burn.loading ? (
          <div className="loading">Loading…</div>
        ) : burnByProject.length === 0 ? (
          <div className="empty">No spend yet in this window.</div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Project</th>
                <th className="num">Input</th>
                <th className="num">Output</th>
                <th className="num">Cache read</th>
                <th className="num">Cost</th>
              </tr>
            </thead>
            <tbody>
              {burnByProject.map((row) => (
                <tr key={row.project_slug ?? "(unattached)"}>
                  <td>
                    {row.project_slug ? (
                      <Link to={`/projects/${row.project_slug}`}>{row.project_slug}</Link>
                    ) : (
                      <span style={{ color: "var(--text-mute)" }}>(unattached)</span>
                    )}
                  </td>
                  <td className="num">{formatTokens(row.input)}</td>
                  <td className="num">{formatTokens(row.output)}</td>
                  <td className="num">{formatTokens(row.cache_read)}</td>
                  <td className="num">{formatCost(row.cost)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <section className="section">
        <h3>Projects</h3>
        {projects.loading ? (
          <div className="loading">Loading…</div>
        ) : (projects.data ?? []).length === 0 ? (
          <div className="empty">No projects registered.</div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Slug</th>
                <th className="num">Agents</th>
                <th className="num">Tasks 7d</th>
                <th>Repo</th>
              </tr>
            </thead>
            <tbody>
              {(projects.data ?? []).map((p) => (
                <tr key={p.slug}>
                  <td>
                    <Link to={`/projects/${p.slug}`}>{p.slug}</Link>
                  </td>
                  <td className="num">{p.agent_count}</td>
                  <td className="num">{p.task_count_7d}</td>
                  <td style={{ fontSize: "0.85rem", color: "var(--text-dim)" }}>
                    {p.github_repo ?? "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <section className="section">
        <h3>Top flags ({windowDays}d)</h3>
        {flags.loading ? (
          <div className="loading">Loading…</div>
        ) : (flags.data ?? []).length === 0 ? (
          <div className="empty">No flags fired in this window.</div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Rule</th>
                <th>Severity</th>
                <th className="num">Count</th>
              </tr>
            </thead>
            <tbody>
              {(flags.data ?? []).map((f) => (
                <tr key={`${f.rule_id}-${f.severity}`}>
                  <td>
                    <code>{f.rule_id}</code>
                  </td>
                  <td>
                    <SeverityBadge severity={f.severity} />
                  </td>
                  <td className="num">{f.count}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </>
  );
}
