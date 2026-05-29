import { useCallback, useEffect, useRef, useState } from "react";

import { unlockAudio } from "../lib/sound";

type Props = {
  onSend: (content: string) => Promise<void>;
};

const MAX_TEXTAREA_PX = 200;

export function Composer({ onSend }: Props) {
  const [value, setValue] = useState("");
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const taRef = useRef<HTMLTextAreaElement | null>(null);

  // Grow the textarea with its content up to a cap, then let it scroll.
  const autoresize = useCallback(() => {
    const el = taRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, MAX_TEXTAREA_PX)}px`;
  }, []);

  useEffect(() => {
    autoresize();
  }, [value, autoresize]);

  const submit = useCallback(async () => {
    const content = value.trim();
    if (!content || sending) return;
    unlockAudio(); // user gesture — unblock the notification sound
    setSending(true);
    setError(null);
    try {
      await onSend(content);
      setValue(""); // clear only on success; on failure the draft is preserved
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setSending(false);
    }
  }, [value, sending, onSend]);

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
        e.preventDefault();
        void submit();
      }
    },
    [submit],
  );

  return (
    <>
      {error ? (
        <div className="error composer-error">
          <span>{error}</span>
          <button type="button" className="retry" onClick={() => void submit()} disabled={sending}>
            Retry
          </button>
        </div>
      ) : null}
      <div className="composer">
        <textarea
          ref={taRef}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder="Message the CTO… (Ctrl/⌘+Enter to send)"
          rows={1}
          disabled={sending}
        />
        <button
          type="button"
          onClick={() => void submit()}
          disabled={sending || !value.trim()}
        >
          {sending ? "…" : "Send"}
        </button>
      </div>
    </>
  );
}
