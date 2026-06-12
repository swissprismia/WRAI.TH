export function SeverityBadge({ severity }: { severity: string }) {
  const known = ["critical", "high", "medium", "low", "info"].includes(severity)
    ? severity
    : "info";
  return <span className={`badge severity ${known}`}>{severity}</span>;
}
