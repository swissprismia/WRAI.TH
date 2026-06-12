export function JsonBlock({ value }: { value: unknown }) {
  if (value === null || value === undefined) {
    return <span style={{ color: "var(--text-mute)" }}>—</span>;
  }
  let pretty: string;
  try {
    pretty = JSON.stringify(value, null, 2);
  } catch {
    pretty = String(value);
  }
  return <pre className="json">{pretty}</pre>;
}
