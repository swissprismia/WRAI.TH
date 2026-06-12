import type { ReactNode } from "react";

export function StatCard({
  label,
  value,
  delta,
}: {
  label: string;
  value: ReactNode;
  delta?: ReactNode;
}) {
  return (
    <div className="card">
      <div className="label">{label}</div>
      <div className="value">{value}</div>
      {delta ? <div className="delta">{delta}</div> : null}
    </div>
  );
}
