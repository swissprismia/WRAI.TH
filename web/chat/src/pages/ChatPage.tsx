import { useEffect, useState } from "react";
import { Navigate, useOutletContext, useParams } from "react-router-dom";

import ChatView from "../components/chat-view";
import type { ChatContext } from "../components/Layout";
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
  const { refreshProjects } = useOutletContext<ChatContext>();

  useEffect(() => {
    setInitialEntries([]);
    setError(null);
    getHistory(slug)
      .then(setInitialEntries)
      .catch((err: unknown) => setError((err as Error).message));
  }, [slug]);

  return (
    <div className="chat-page">
      <header className="chat-header">
        <h2>{slug}</h2>
      </header>
      {error ? <div className="error">Relay error: {error}</div> : null}
      {/* key={slug} remounts the view on conversation switch — fresh entries,
          scroll position, and poll cursor. */}
      <ChatView
        key={slug}
        slug={slug}
        initialEntries={initialEntries}
        pollIntervalMs={POLL_INTERVAL_MS}
        onMarkedRead={refreshProjects}
      />
    </div>
  );
}
