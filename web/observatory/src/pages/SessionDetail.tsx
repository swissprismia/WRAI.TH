import { Link, useParams } from "react-router-dom";

import { JsonBlock } from "../components/JsonBlock";
import { SeverityBadge } from "../components/SeverityBadge";
import { StatCard } from "../components/StatCard";
import { formatCost, formatDateTime, formatDurationMs, formatTokens } from "../lib/format";
import { useApi } from "../lib/api";
import type { EventRow, FlagRow, SessionDetail as SessionDetailT, TokenDeltaRow } from "../lib/api";

function summarize(e: EventRow): string {
  if (e.path) return e.path;
  if (e.tool && e.input && typeof e.input === "object") {
    const command = (e.input as { command?: string }).command;
    if (typeof command === "string") return command.slice(0, 120);
  }
  return "";
}

export default function SessionDetail() {
  const { id = "" } = useParams();

  const session = useApi<SessionDetailT>(id ? `/sessions/${encodeURIComponent(id)}` : null);
  const events = useApi<EventRow[]>(id ? `/sessions/${encodeURIComponent(id)}/events?limit=500` : null);
  const deltas = useApi<TokenDeltaRow[]>(
    id ? `/sessions/${encodeURIComponent(id)}/token_deltas?limit=500` : null,
  );
  const flags = useApi<FlagRow[]>(id ? `/sessions/${encodeURIComponent(id)}/flags` : null);

  if (session.error) {
    return (
      <>
        <header className="page-header">
          <h2>Session</h2>
        </header>
        <div className="error">Session not found: {session.error}</div>
      </>
    );
  }

  const s = session.data;
  const eventRows = events.data ?? [];
  const deltaRows = deltas.data ?? [];
  const flagRows = flags.data ?? [];

  const flagsByEvent = new Map<number, FlagRow[]>();
  for (const f of flagRows) {
    const list = flagsByEvent.get(f.event_id) ?? [];
    list.push(f);
    flagsByEvent.set(f.event_id, list);
  }

  const totals = deltaRows.reduce(
    (acc, d) => ({
      input: acc.input + d.input_tokens,
      output: acc.output + d.output_tokens,
      cache_read: acc.cache_read + d.cache_read_input_tokens,
      cache_5m: acc.cache_5m + d.cache_creation_input_tokens_5m,
      cache_1h: acc.cache_1h + d.cache_creation_input_tokens_1h,
      cost: acc.cost + (d.cost_usd ?? 0),
    }),
    { input: 0, output: 0, cache_read: 0, cache_5m: 0, cache_1h: 0, cost: 0 },
  );

  return (
    <>
      <header className="page-header">
        <h2>Session</h2>
        <div className="subtitle">
          <code>{s?.session_id ?? id}</code>
          {s?.profile_slug ? (
            <>
              {" · "}
              <Link to={`/agents/${s.profile_slug}`}>{s.profile_slug}</Link>
            </>
          ) : null}
          {s?.project_slug ? (
            <>
              {" · "}
              <Link to={`/projects/${s.project_slug}`}>{s.project_slug}</Link>
            </>
          ) : null}
          {s?.mode ? ` · ${s.mode}` : ""}
        </div>
      </header>

      <div className="cards">
        <StatCard label="Started" value={formatDateTime(s?.started_at)} />
        <StatCard label="Duration" value={formatDurationMs(s?.duration_ms)} />
        <StatCard label="Turns" value={s?.turns ?? "—"} />
        <StatCard label="Cost" value={formatCost(totals.cost)} />
        <StatCard
          label="Tokens (in / out)"
          value={
            <span>
              {formatTokens(totals.input)} <span style={{ color: "var(--text-mute)" }}>/</span>{" "}
              {formatTokens(totals.output)}
            </span>
          }
          delta={`cache: ${formatTokens(totals.cache_read)} read · ${formatTokens(totals.cache_5m + totals.cache_1h)} write`}
        />
      </div>

      {flagRows.length > 0 ? (
        <section className="section">
          <h3>Flags ({flagRows.length})</h3>
          <table>
            <thead>
              <tr>
                <th>Rule</th>
                <th>Severity</th>
                <th>Captured</th>
                <th>Event</th>
              </tr>
            </thead>
            <tbody>
              {flagRows.map((f) => (
                <tr key={f.flag_id}>
                  <td>
                    <code>{f.rule_id}</code>
                  </td>
                  <td>
                    <SeverityBadge severity={f.severity} />
                  </td>
                  <td>{formatDateTime(f.captured_at)}</td>
                  <td>
                    <code>#{f.event_id}</code>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      ) : null}

      <section className="section">
        <h3>Token usage per turn</h3>
        {deltas.loading ? (
          <div className="loading">Loading…</div>
        ) : deltaRows.length === 0 ? (
          <div className="empty">No usage recorded yet.</div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Turn</th>
                <th>Message</th>
                <th>Model</th>
                <th>Time</th>
                <th className="num">Input</th>
                <th className="num">Output</th>
                <th className="num">Cache R</th>
                <th className="num">Cache 5m</th>
                <th className="num">Cache 1h</th>
                <th className="num">Cost</th>
              </tr>
            </thead>
            <tbody>
              {deltaRows.map((d) => (
                <tr key={d.id}>
                  <td className="num">{d.turn_index ?? "—"}</td>
                  <td>{d.message_id ? <code>{d.message_id.slice(0, 12)}</code> : "—"}</td>
                  <td>
                    <code>{d.model}</code>
                  </td>
                  <td>{formatDateTime(d.ts)}</td>
                  <td className="num">{formatTokens(d.input_tokens)}</td>
                  <td className="num">{formatTokens(d.output_tokens)}</td>
                  <td className="num">{formatTokens(d.cache_read_input_tokens)}</td>
                  <td className="num">{formatTokens(d.cache_creation_input_tokens_5m)}</td>
                  <td className="num">{formatTokens(d.cache_creation_input_tokens_1h)}</td>
                  <td className="num">{formatCost(d.cost_usd, true)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <section className="section">
        <h3>Timeline ({eventRows.length} events)</h3>
        {events.loading ? (
          <div className="loading">Loading…</div>
        ) : eventRows.length === 0 ? (
          <div className="empty">No events recorded.</div>
        ) : (
          <div className="timeline">
            {eventRows.map((e) => (
              <details key={e.event_id} className="timeline-row">
                <summary
                  style={{
                    display: "grid",
                    gridTemplateColumns: "8.5rem 6.5rem 9rem 1fr",
                    gap: "0.75rem",
                    alignItems: "baseline",
                  }}
                >
                  <span className="ts">{formatDateTime(e.ts).slice(11, 19)}</span>
                  <span className="kind">{e.kind}</span>
                  <span className="tool">{e.tool ?? ""}</span>
                  <span className="summary">
                    {summarize(e)}
                    {flagsByEvent.has(e.event_id) ? (
                      <span style={{ marginLeft: "0.5rem" }}>
                        {flagsByEvent.get(e.event_id)!.map((f) => (
                          <span key={f.flag_id} style={{ marginRight: "0.25rem" }}>
                            <SeverityBadge severity={f.severity} />
                          </span>
                        ))}
                      </span>
                    ) : null}
                  </span>
                </summary>
                <div style={{ marginTop: "0.5rem" }}>
                  {e.input ? (
                    <div>
                      <div style={{ color: "var(--text-dim)", fontSize: "0.75rem" }}>input</div>
                      <JsonBlock value={e.input} />
                    </div>
                  ) : null}
                  {e.output ? (
                    <div style={{ marginTop: "0.4rem" }}>
                      <div style={{ color: "var(--text-dim)", fontSize: "0.75rem" }}>output</div>
                      <JsonBlock value={e.output} />
                    </div>
                  ) : null}
                </div>
              </details>
            ))}
          </div>
        )}
      </section>
    </>
  );
}
