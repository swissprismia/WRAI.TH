import { useEffect, useState } from "react";
import { Link, Navigate, useParams } from "react-router-dom";

import ChatView from "../components/chat-view";
import { getHistory, type ChatEntry } from "../lib/api";

const SLUG_RE = /^[a-z0-9][a-z0-9-]{0,63}$/;

const POLL_INTERVAL_MS = 5000;

export default function ChatPage() {
  const { slug } = useParams<{ slug: string }>();

  if (!slug || !SLUG_RE.test(slug)) {
    return <Navigate to="/" replace />;
  }

  return <ChatPageInner slug={slug} />;
}

function ChatPageInner({ slug }: { slug: string }) {
  const [initialEntries, setInitialEntries] = useState<ChatEntry[]>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    getHistory(slug)
      .then(setInitialEntries)
      .catch((err: unknown) => setError((err as Error).message));
  }, [slug]);

  return (
    <div className="shell">
      <header className="header">
        <h1>
          <Link to="/">CTO Chat</Link>
        </h1>
      </header>
      <div style={{ marginBottom: "0.75rem" }}>
        <Link className="back-link" to="/">
          Projects
        </Link>
        <h2 style={{ margin: "0.5rem 0 0", fontSize: "1.25rem" }}>{slug}</h2>
      </div>
      {error ? <div className="error">Relay error: {error}</div> : null}
      <ChatView
        slug={slug}
        initialEntries={initialEntries}
        pollIntervalMs={POLL_INTERVAL_MS}
      />
    </div>
  );
}
