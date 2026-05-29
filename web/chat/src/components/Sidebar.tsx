import { Link } from "react-router-dom";

import type { Project } from "../lib/api";

function relativeTime(iso?: string): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const mins = Math.floor((Date.now() - t) / 60000);
  if (mins < 1) return "now";
  if (mins < 60) return `${mins}m`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h`;
  const days = Math.floor(hrs / 24);
  if (days < 7) return `${days}d`;
  return `${Math.floor(days / 7)}w`;
}

type Props = {
  projects: Project[];
  activeSlug: string | null;
  error: string | null;
};

export default function Sidebar({ projects, activeSlug, error }: Props) {
  return (
    <aside className="sidebar">
      <div className="sidebar-head">
        <h1>CTO Chat</h1>
      </div>
      {error ? <div className="error sidebar-error">{error}</div> : null}
      {projects.length === 0 && !error ? (
        <div className="sidebar-empty">No conversations yet.</div>
      ) : (
        <nav className="conv-list">
          {projects.map((p) => {
            const active = p.slug === activeSlug;
            const unread = !active && p.unread > 0;
            return (
              <Link
                key={p.slug}
                to={`/p/${p.slug}`}
                className={`conv${active ? " conv--active" : ""}${unread ? " conv--unread" : ""}`}
              >
                <div className="conv-top">
                  <span className="conv-name">{p.slug}</span>
                  <span className="conv-time">{relativeTime(p.latest_ts)}</span>
                </div>
                <div className="conv-bottom">
                  <span className="conv-preview">
                    {p.last_preview
                      ? `${p.last_kind === "human" ? "You: " : ""}${p.last_preview}`
                      : p.executive_role}
                  </span>
                  {unread ? <span className="badge">{p.unread > 99 ? "99+" : p.unread}</span> : null}
                </div>
              </Link>
            );
          })}
        </nav>
      )}
    </aside>
  );
}
