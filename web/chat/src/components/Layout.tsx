import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Outlet, useLocation } from "react-router-dom";

import { listProjects, type Project } from "../lib/api";
import { playBlip } from "../lib/sound";
import Sidebar from "./Sidebar";

const PROJECTS_POLL_MS = 8000;

export type ChatContext = { refreshProjects: () => void };

function activeSlugFromPath(pathname: string): string | null {
  const m = pathname.match(/^\/p\/([a-z0-9][a-z0-9-]{0,63})/i);
  return m ? m[1] : null;
}

export default function Layout() {
  const [projects, setProjects] = useState<Project[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const location = useLocation();
  const activeSlug = activeSlugFromPath(location.pathname);
  const prevUnreadRef = useRef(0);

  const refreshProjects = useCallback(async () => {
    try {
      const list = await listProjects();
      setProjects(list);
      setError(null);
      const total = list.reduce((s, p) => s + (p.unread || 0), 0);
      // Beep only when total unread *grows* while the tab is hidden — covers both
      // the active conversation and the others, from this single poll.
      if (total > prevUnreadRef.current && typeof document !== "undefined" && document.hidden) {
        playBlip();
      }
      prevUnreadRef.current = total;
    } catch (err) {
      setError((err as Error).message);
    }
  }, []);

  useEffect(() => {
    void refreshProjects();
    const t = setInterval(() => void refreshProjects(), PROJECTS_POLL_MS);
    return () => clearInterval(t);
  }, [refreshProjects]);

  // Tab title: active project name + an unread badge. The active conversation is
  // excluded from the badge — you're already looking at it.
  const badge = useMemo(() => {
    const total = projects.reduce((s, p) => s + (p.unread || 0), 0);
    const active = activeSlug ? projects.find((p) => p.slug === activeSlug)?.unread ?? 0 : 0;
    return Math.max(0, total - active);
  }, [projects, activeSlug]);

  useEffect(() => {
    const base = activeSlug ? `${activeSlug} · CTO Chat` : "CTO Chat";
    document.title = badge > 0 ? `(${badge}) ${base}` : base;
  }, [activeSlug, badge]);

  // Collapse the mobile drawer whenever the route changes.
  useEffect(() => {
    setSidebarOpen(false);
  }, [location.pathname]);

  return (
    <div className={`app${sidebarOpen ? " app--sidebar-open" : ""}`}>
      {sidebarOpen ? <div className="scrim" onClick={() => setSidebarOpen(false)} /> : null}
      <Sidebar projects={projects} activeSlug={activeSlug} error={error} />
      <main className="main">
        <button
          type="button"
          className="sidebar-toggle"
          aria-label="Toggle conversations"
          onClick={() => setSidebarOpen((v) => !v)}
        >
          ☰
        </button>
        <Outlet context={{ refreshProjects } satisfies ChatContext} />
      </main>
    </div>
  );
}
