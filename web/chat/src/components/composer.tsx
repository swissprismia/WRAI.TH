import { useCallback, useState } from "react";

type Props = {
  onSend: (content: string) => Promise<void>;
};

export function Composer({ onSend }: Props) {
  const [value, setValue] = useState("");
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = useCallback(async () => {
    const content = value.trim();
    if (!content || sending) return;
    setSending(true);
    setError(null);
    try {
      await onSend(content);
      setValue("");
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
      {error ? <div className="error">{error}</div> : null}
      <div className="composer">
        <textarea
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder="Message the CTO… (Ctrl/⌘+Enter to send)"
          rows={2}
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
