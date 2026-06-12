/**
 * Pure display formatters. No framework dependencies — usable anywhere.
 * Ported verbatim from the retired standalone observatory-ui.
 */

const COMPACT = new Intl.NumberFormat("en-US", {
  notation: "compact",
  maximumFractionDigits: 1,
});

const USD = new Intl.NumberFormat("en-US", {
  style: "currency",
  currency: "USD",
  maximumFractionDigits: 2,
});

const USD_PRECISE = new Intl.NumberFormat("en-US", {
  style: "currency",
  currency: "USD",
  maximumFractionDigits: 4,
});

const PERCENT = new Intl.NumberFormat("en-US", {
  style: "percent",
  maximumFractionDigits: 1,
});

export function formatTokens(value: string | number | null | undefined): string {
  if (value === null || value === undefined) return "—";
  const n = typeof value === "string" ? Number.parseFloat(value) : value;
  if (Number.isNaN(n)) return "—";
  return COMPACT.format(n);
}

export function formatCost(value: string | number | null | undefined, precise = false): string {
  if (value === null || value === undefined) return "—";
  const n = typeof value === "string" ? Number.parseFloat(value) : value;
  if (Number.isNaN(n)) return "—";
  return precise ? USD_PRECISE.format(n) : USD.format(n);
}

export function formatPercent(value: string | number | null | undefined): string {
  if (value === null || value === undefined) return "—";
  const n = typeof value === "string" ? Number.parseFloat(value) : value;
  if (Number.isNaN(n)) return "—";
  return PERCENT.format(n);
}

export function formatDateTime(value: string | null | undefined): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toISOString().replace("T", " ").replace(/\.\d+Z$/, "Z");
}

export function formatDuration(seconds: number | string | null | undefined): string {
  if (seconds === null || seconds === undefined) return "—";
  const s = typeof seconds === "string" ? Number.parseFloat(seconds) : seconds;
  if (Number.isNaN(s) || s < 0) return "—";
  if (s < 60) return `${Math.round(s)}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${Math.round(s % 60)}s`;
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  return `${h}h ${m}m`;
}

export function formatDurationMs(ms: number | string | null | undefined): string {
  if (ms === null || ms === undefined) return "—";
  const n = typeof ms === "string" ? Number.parseFloat(ms) : ms;
  if (Number.isNaN(n) || n < 0) return "—";
  return formatDuration(n / 1000);
}
