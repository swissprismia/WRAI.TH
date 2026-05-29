export interface Project {
  slug: string;
  executive_role: string;
  latest_ts?: string;
  last_preview?: string;
  last_kind?: "human" | "cto";
  unread: number;
}

export interface ChatEntry {
  id: string;
  ts: string;
  kind: "human" | "cto";
  from: string;
  content: string;
}

export async function listProjects(): Promise<Project[]> {
  const resp = await fetch("/chat/api/projects", { cache: "no-store" });
  if (!resp.ok) throw new Error(`projects HTTP ${resp.status}`);
  const data = (await resp.json()) as { projects?: Project[] };
  return data.projects ?? [];
}

export async function sendMessage(slug: string, content: string): Promise<ChatEntry> {
  const resp = await fetch(`/chat/api/p/${encodeURIComponent(slug)}/send`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ content }),
  });
  if (!resp.ok) {
    const body = await resp.text().catch(() => "");
    throw new Error(`send HTTP ${resp.status}${body ? `: ${body}` : ""}`);
  }
  const data = (await resp.json()) as { entry?: ChatEntry; error?: string };
  if (data.error) throw new Error(data.error);
  if (!data.entry) throw new Error("no entry in send response");
  return data.entry;
}

export async function pollMessages(slug: string, since: string | null): Promise<ChatEntry[]> {
  const url = `/chat/api/p/${encodeURIComponent(slug)}/poll${
    since ? `?since=${encodeURIComponent(since)}` : ""
  }`;
  const resp = await fetch(url, { cache: "no-store" });
  if (!resp.ok) {
    const body = await resp.text().catch(() => "");
    throw new Error(`poll HTTP ${resp.status}${body ? `: ${body}` : ""}`);
  }
  const data = (await resp.json()) as { messages?: ChatEntry[]; error?: string };
  if (data.error) throw new Error(data.error);
  return data.messages ?? [];
}

export async function getHistory(
  slug: string,
  before?: string | null,
  limit?: number,
): Promise<ChatEntry[]> {
  const params = new URLSearchParams();
  if (before) params.set("before", before);
  if (limit) params.set("limit", String(limit));
  const qs = params.toString();
  const resp = await fetch(
    `/chat/api/p/${encodeURIComponent(slug)}/history${qs ? `?${qs}` : ""}`,
    { cache: "no-store" },
  );
  if (!resp.ok) {
    const body = await resp.text().catch(() => "");
    throw new Error(`history HTTP ${resp.status}${body ? `: ${body}` : ""}`);
  }
  const data = (await resp.json()) as { messages?: ChatEntry[]; error?: string };
  if (data.error) throw new Error(data.error);
  return data.messages ?? [];
}

// markRead clears a project's unread badge. Best-effort: failures are swallowed
// so a transient relay hiccup never blocks the chat.
export async function markRead(slug: string): Promise<void> {
  try {
    await fetch(`/chat/api/p/${encodeURIComponent(slug)}/read`, { method: "POST" });
  } catch {
    /* ignore */
  }
}
