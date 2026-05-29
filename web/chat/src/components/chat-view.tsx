import { Fragment, useCallback, useEffect, useRef, useState } from "react";
import Markdown from "react-markdown";
import type { Components } from "react-markdown";
import remarkGfm from "remark-gfm";

import { getHistory, markRead, pollMessages, sendMessage, type ChatEntry } from "../lib/api";
import { Composer } from "./composer";

const HIDDEN_POLL_INTERVAL_MS = 30_000;
const PAGE_SIZE = 50;
const NEAR_BOTTOM_PX = 80;

// Open links from the CTO's Markdown in a new tab so a click never navigates
// away from the chat.
const MD_COMPONENTS: Components = {
  a: ({ node: _node, ...props }) => <a {...props} target="_blank" rel="noopener noreferrer" />,
};

function formatClock(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  return `${hh}:${mm}`;
}

function sameDay(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  );
}

function dayLabel(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const today = new Date();
  const yesterday = new Date();
  yesterday.setDate(today.getDate() - 1);
  if (sameDay(d, today)) return "Today";
  if (sameDay(d, yesterday)) return "Yesterday";
  return d.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: d.getFullYear() === today.getFullYear() ? undefined : "numeric",
  });
}

type Props = {
  slug: string;
  initialEntries: ChatEntry[];
  pollIntervalMs: number;
  onMarkedRead?: () => void;
};

export default function ChatView({ slug, initialEntries, pollIntervalMs, onMarkedRead }: Props) {
  const [entries, setEntries] = useState<ChatEntry[]>(initialEntries);
  const [error, setError] = useState<string | null>(null);
  const [hasMore, setHasMore] = useState(initialEntries.length >= PAGE_SIZE);
  const [loadingMore, setLoadingMore] = useState(false);
  const [showJump, setShowJump] = useState(false);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const lastTsRef = useRef<string | null>(initialEntries.at(-1)?.ts ?? null);
  const newestIdRef = useRef<string | null>(initialEntries.at(-1)?.id ?? null);
  const nearBottomRef = useRef(true);
  const inFlightRef = useRef(false);

  const scrollToBottom = useCallback(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
    nearBottomRef.current = true;
    setShowJump(false);
  }, []);

  // Tell the relay we've seen the CTO's messages (clears the unread badge) — but
  // only while the tab is actually visible.
  const markSeen = useCallback(() => {
    if (typeof document !== "undefined" && document.hidden) return;
    void markRead(slug).then(() => onMarkedRead?.());
  }, [slug, onMarkedRead]);

  const onScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    const near = el.scrollHeight - el.scrollTop - el.clientHeight < NEAR_BOTTOM_PX;
    nearBottomRef.current = near;
    if (near) setShowJump(false);
  }, []);

  const mergeEntries = useCallback((incoming: ChatEntry[]) => {
    if (incoming.length === 0) return;
    setEntries((prev) => {
      const seen = new Set(prev.map((e) => e.id));
      const merged = [...prev];
      for (const e of incoming) {
        if (!seen.has(e.id)) merged.push(e);
      }
      merged.sort((a, b) => Date.parse(a.ts) - Date.parse(b.ts));
      const newest = merged.at(-1);
      if (newest) lastTsRef.current = newest.ts;
      return merged;
    });
  }, []);

  // When a *newer* message lands: auto-scroll if the reader is already at the
  // bottom (or it's their own message), otherwise surface the jump button. Either
  // way, mark the conversation seen. Prepending older history (Load older) does
  // not change the newest id, so it never triggers this.
  useEffect(() => {
    const newest = entries.at(-1);
    if (!newest || newest.id === newestIdRef.current) return;
    newestIdRef.current = newest.id;
    if (nearBottomRef.current || newest.kind === "human") {
      scrollToBottom();
    } else {
      setShowJump(true);
    }
    markSeen();
  }, [entries, scrollToBottom, markSeen]);

  // History arrives asynchronously via the initialEntries prop after mount;
  // merge it (deduped by id) and seed the "has more" flag.
  useEffect(() => {
    mergeEntries(initialEntries);
    if (initialEntries.length > 0) setHasMore(initialEntries.length >= PAGE_SIZE);
  }, [initialEntries, mergeEntries]);

  const loadOlder = useCallback(async () => {
    if (loadingMore) return;
    const oldest = entries[0];
    if (!oldest) return;
    setLoadingMore(true);
    try {
      // `before` is exclusive server-side (created_at < before), so the cursor
      // message is never re-fetched; mergeEntries dedupes regardless.
      const older = await getHistory(slug, oldest.ts, PAGE_SIZE);
      if (older.length < PAGE_SIZE) setHasMore(false);
      mergeEntries(older);
      setError(null);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setLoadingMore(false);
    }
  }, [entries, loadingMore, slug, mergeEntries]);

  const pollOnce = useCallback(async () => {
    if (inFlightRef.current) return;
    inFlightRef.current = true;
    try {
      const msgs = await pollMessages(slug, lastTsRef.current);
      mergeEntries(msgs);
      setError(null);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      inFlightRef.current = false;
    }
  }, [slug, mergeEntries]);

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null;
    let cancelled = false;
    const schedule = () => {
      if (cancelled) return;
      const hidden = typeof document !== "undefined" && document.visibilityState === "hidden";
      const wait = hidden ? HIDDEN_POLL_INTERVAL_MS : pollIntervalMs;
      timer = setTimeout(async () => {
        if (cancelled) return;
        await pollOnce();
        schedule();
      }, wait);
    };
    void pollOnce().then(schedule);
    const onVisibility = () => {
      if (document.visibilityState === "visible") {
        if (timer) clearTimeout(timer);
        markSeen();
        void pollOnce().then(schedule);
      }
    };
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [pollOnce, pollIntervalMs, markSeen]);

  const handleSend = useCallback(
    async (content: string) => {
      const entry = await sendMessage(slug, content);
      mergeEntries([entry]);
    },
    [slug, mergeEntries],
  );

  return (
    <div className="chat-view">
      <div className="messages" ref={scrollRef} onScroll={onScroll}>
        {hasMore && entries.length > 0 ? (
          <button
            type="button"
            className="load-more"
            onClick={() => void loadOlder()}
            disabled={loadingMore}
          >
            {loadingMore ? "Loading…" : "Load older messages"}
          </button>
        ) : null}
        {entries.length === 0 ? (
          <div className="empty">{`No messages yet. Say hi to the CTO on ${slug}.`}</div>
        ) : (
          entries.map((e, i) => {
            const showDay = i === 0 || dayLabel(entries[i - 1].ts) !== dayLabel(e.ts);
            return (
              <Fragment key={e.id}>
                {showDay ? (
                  <div className="day-sep">
                    <span>{dayLabel(e.ts)}</span>
                  </div>
                ) : null}
                <div className={`message ${e.kind}`}>
                  <div className="meta">
                    <span className="who">{e.from}</span>
                    <span>{formatClock(e.ts)}</span>
                  </div>
                  {e.kind === "cto" ? (
                    <div className="body markdown">
                      <Markdown remarkPlugins={[remarkGfm]} components={MD_COMPONENTS}>
                        {e.content}
                      </Markdown>
                    </div>
                  ) : (
                    <div className="body">{e.content}</div>
                  )}
                </div>
              </Fragment>
            );
          })
        )}
      </div>
      {showJump ? (
        <button type="button" className="jump-latest" onClick={scrollToBottom}>
          ↓ New messages
        </button>
      ) : null}
      {error ? <div className="error">{error}</div> : null}
      <Composer onSend={handleSend} />
    </div>
  );
}
