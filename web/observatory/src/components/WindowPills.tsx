import { useSearchParams } from "react-router-dom";

import type { WindowDays } from "../lib/api";

const WINDOWS: WindowDays[] = [1, 7, 30];

// WindowPills toggles the ?window= search param (1 / 7 / 30 days), preserving
// any other params. Pages read the active window via parseWindow(searchParams).
export function WindowPills({ active }: { active: WindowDays }) {
  const [params, setParams] = useSearchParams();
  return (
    <div className="filters">
      {WINDOWS.map((days) => (
        <button
          key={days}
          type="button"
          className={`pill${days === active ? " active" : ""}`}
          onClick={() => {
            const next = new URLSearchParams(params);
            next.set("window", String(days));
            setParams(next);
          }}
        >
          {days === 1 ? "Today" : `${days}d`}
        </button>
      ))}
    </div>
  );
}
