import { StatCard } from "../components/StatCard";
import { formatCost, formatTokens } from "../lib/format";
import { useApi } from "../lib/api";
import type { Budget, BurnRow } from "../lib/api";

export default function Budgets() {
  const budgets = useApi<Budget[]>("/budgets");
  const burn30d = useApi<BurnRow[]>("/burn?window=30");

  const profiles = budgets.data ?? [];
  const burnRows = burn30d.data ?? [];

  const dailyTotal = profiles.reduce((sum, p) => sum + p.daily_cost_usd, 0);
  const dailyTokens = profiles.reduce(
    (sum, p) => sum + p.daily_input_tokens + p.daily_output_tokens,
    0,
  );
  const monthlyTotal = burnRows.reduce((sum, b) => sum + b.cost_usd, 0);
  // Crude projection: today's burn × 30. ADF-018 owns the real budget
  // envelopes; this surface is a visibility tool, not a clamp.
  const projected = dailyTotal * 30;

  return (
    <>
      <header className="page-header">
        <h2>Budgets</h2>
        <div className="subtitle">
          Per-role daily caps live in <code>ADF-018</code>; this dashboard shows actual burn so the
          envelopes can be tuned.
        </div>
      </header>

      <div className="cards">
        <StatCard label="Today (cost)" value={formatCost(dailyTotal)} />
        <StatCard label="Today (tokens)" value={formatTokens(dailyTokens)} />
        <StatCard label="30d (cost)" value={formatCost(monthlyTotal)} />
        <StatCard label="Projected month" value={formatCost(projected)} delta="today × 30" />
      </div>

      <section className="section">
        <h3>Today's burn by profile</h3>
        {budgets.loading ? (
          <div className="loading">Loading…</div>
        ) : profiles.length === 0 ? (
          <div className="empty">No spend today.</div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Profile</th>
                <th className="num">Agents</th>
                <th className="num">Input tokens</th>
                <th className="num">Output tokens</th>
                <th className="num">Cost today</th>
              </tr>
            </thead>
            <tbody>
              {profiles.map((p) => (
                <tr key={p.profile_slug}>
                  <td>{p.profile_slug}</td>
                  <td className="num">{p.agent_count}</td>
                  <td className="num">{formatTokens(p.daily_input_tokens)}</td>
                  <td className="num">{formatTokens(p.daily_output_tokens)}</td>
                  <td className="num">{formatCost(p.daily_cost_usd)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <section className="section">
        <h3>30-day burn by model</h3>
        {burn30d.loading ? (
          <div className="loading">Loading…</div>
        ) : burnRows.length === 0 ? (
          <div className="empty">No spend in the last 30 days.</div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Day</th>
                <th>Project</th>
                <th>Model</th>
                <th className="num">Input</th>
                <th className="num">Output</th>
                <th className="num">Cost</th>
              </tr>
            </thead>
            <tbody>
              {burnRows.slice(0, 100).map((b, i) => (
                <tr key={`${b.day_bucket}-${b.model}-${i}`}>
                  <td>{b.day_bucket}</td>
                  <td>{b.project_slug ?? "—"}</td>
                  <td>
                    <code>{b.model}</code>
                  </td>
                  <td className="num">{formatTokens(b.input_tokens)}</td>
                  <td className="num">{formatTokens(b.output_tokens)}</td>
                  <td className="num">{formatCost(b.cost_usd)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </>
  );
}
