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
  const [loaded, setLoaded] = useState<{ slug: string; entries: ChatEntry[] }>({
    slug,
    entries: [],
  });
  const [error, setError] = useState<string | null>(null);
  const { refreshProjects } = useOutletContext<ChatContext>();

  useEffect(() => {
    let cancelled = false;
    setError(null);
    getHistory(slug)
      .then((entries) => {
        if (!cancelled) setLoaded({ slug, entries });
      })
      .catch((err: unknown) => {
        if (!cancelled) setError((err as Error).message);
      });
    return () => {
      cancelled = true;
    };
  }, [slug]);

  // The slug switches one render before the effect refetches, so `loaded` still
  // holds the previous conversation's entries here. Feed the view an empty list
  // until this slug's history lands — otherwise the remounted ChatView seeds its
  // state from stale entries and shows the old conversation's messages.
  const initialEntries = loaded.slug === slug ? loaded.entries : [];

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
