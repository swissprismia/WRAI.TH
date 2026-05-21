import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { listProjects, type Project } from "../lib/api";

export default function ProjectList() {
  const [projects, setProjects] = useState<Project[]>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    listProjects()
      .then(setProjects)
      .catch((err: unknown) => setError((err as Error).message));
  }, []);

  return (
    <div className="shell">
      <header className="header">
        <h1>CTO Chat</h1>
      </header>
      {error ? <div className="error">Relay error: {error}</div> : null}
      {projects.length === 0 && !error ? (
        <div className="empty">No projects registered with the relay yet.</div>
      ) : (
        <div className="project-list">
          {projects.map((p) => (
            <Link key={p.slug} className="project-card" to={`/p/${p.slug}`}>
              <span className="name">{p.name ?? p.slug}</span>
              <span className="slug">{`${p.slug}-${p.executive_role}`}</span>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
