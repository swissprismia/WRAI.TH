import { useCallback, useEffect, useRef, useState } from "react";

import { pollMessages, sendMessage, type ChatEntry } from "../lib/api";
import { Composer } from "./composer";

const HIDDEN_POLL_INTERVAL_MS = 30_000;

function formatClock(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  return `${hh}:${mm}`;
}

type Props = {
  slug: string;
  initialEntries: ChatEntry[];
  pollIntervalMs: number;
};

export default function ChatView({ slug, initialEntries, pollIntervalMs }: Props) {
  const [entries, setEntries] = useState<ChatEntry[]>(initialEntries);
  const [error, setError] = useState<string | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const lastTsRef = useRef<string | null>(initialEntries.at(-1)?.ts ?? null);
  const inFlightRef = useRef(false);

  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [entries.length]);

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

  // History is fetched asynchronously by the parent (ChatPage) and arrives via
  // the initialEntries prop *after* this component has already mounted. useState
  // only captures the prop's first (empty) value, so merge it in whenever it
  // changes. Without this, loaded history is silently dropped and only the
  // rolling 5-min poll window ever populates the view — messages vanish on tab
  // reopen even though the relay returns them (ADF-082 / CodeFire #99).
  // mergeEntries dedupes by id, so this is safe against what poll already added.
  useEffect(() => {
    mergeEntries(initialEntries);
  }, [initialEntries, mergeEntries]);

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
        void pollOnce().then(schedule);
      }
    };
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [pollOnce, pollIntervalMs]);

  const handleSend = useCallback(
    async (content: string) => {
      const entry = await sendMessage(slug, content);
      mergeEntries([entry]);
    },
    [slug, mergeEntries],
  );

  return (
    <div className="chat-view">
      <div className="messages" ref={scrollRef}>
        {entries.length === 0 ? (
          <div className="empty">{`No messages yet. Say hi to the CTO on ${slug}.`}</div>
        ) : (
          entries.map((e) => (
            <div key={e.id} className={`message ${e.kind}`}>
              <div className="meta">
                <span className="who">{e.from}</span>
                <span>{formatClock(e.ts)}</span>
              </div>
              <div>{e.content}</div>
            </div>
          ))
        )}
      </div>
      {error ? <div className="error">{error}</div> : null}
      <Composer onSend={handleSend} />
    </div>
  );
}
