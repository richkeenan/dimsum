import { useState } from "react";
import { count, text, outcomes, type Row, type Point } from "@/lib/api";
import { DataTable } from "@/components/data";
const labels: Record<(typeof outcomes)[number], string> = {
  local: "Local answer",
  blocked: "Blocked",
  cache: "Cached",
  stale: "Cached (stale)",
  forwarded: "Forwarded",
  error: "Failed",
  rejected: "Rejected",
};
export default function TrafficChart({ buckets }: { buckets: Point[] }) {
  const [table, setTable] = useState(false);
  const values = buckets.map((b) =>
    outcomes.map((k) => {
      const n = Number(b.outcomes?.[k]);
      return !b.gap && Number.isFinite(n) && n >= 0 ? n : 0;
    }),
  );
  const max = Math.max(1, ...values.map((v) => v.reduce((a, b) => a + b, 0)));
  return (
    <>
      <div className="flex flex-wrap items-center gap-2.5 px-5 py-[18px] text-[11px] text-muted-foreground min-[701px]:gap-[18px]">
        {outcomes.map((k) => (
          <span className="flex items-center gap-[5px]" key={k}>
            <i
              className={`size-2 rounded-[2px] ${k === "forwarded" ? "bg-blue-600" : k === "blocked" ? "bg-slate-500" : k === "cache" ? "bg-blue-300" : k === "local" ? "bg-sky-500" : k === "error" ? "bg-indigo-800" : k === "rejected" ? "bg-violet-400" : "bg-slate-300"}`}
            />
            {labels[k]}
          </span>
        ))}
        <span className="flex items-center gap-[5px]">
          <i className="size-2 rounded-[2px] bg-[repeating-linear-gradient(135deg,transparent,transparent_4px,var(--border)_4px,var(--border)_5px)]" />
          Missing interval
        </span>
      </div>
      {buckets.length ? (
        <div
          className={`mx-[5px] mb-3 flex h-[145px] items-end border-b border-border bg-[repeating-linear-gradient(to_top,transparent,transparent_45px,var(--muted)_45px,var(--muted)_46px)] px-5 pt-[5px] min-[701px]:mx-5 min-[701px]:mb-[18px] min-[701px]:h-[140px] min-[1450px]:h-[230px] ${buckets.length > 100 ? "gap-0" : "gap-[3px]"}`}
          role="img"
          aria-label="DNS outcomes over time. Exact values are available in the traffic table below."
        >
          {buckets.map((b, i) => (
            <div
              className={
                "h-full min-w-0 max-w-[50px] flex-1 " +
                (b.gap || !b.outcomes || b.complete === false
                  ? "bg-[repeating-linear-gradient(135deg,transparent,transparent_4px,var(--border)_4px,var(--border)_5px)]"
                  : "")
              }
              key={i}
              title={`${b.time}: ${b.gap || !b.outcomes ? "Missing interval" : (b.complete === false ? "Partial coverage. " : "") + outcomes.map((k) => `${labels[k]}: ${count(b.outcomes?.[k])}`).join(", ")}`}
            >
              {!b.gap && b.outcomes && (
                <svg
                  className="h-full w-full"
                  viewBox="0 0 1 100"
                  preserveAspectRatio="none"
                  aria-hidden="true"
                >
                  {outcomes.map((k, j) => (
                    <rect
                      key={k}
                      className={
                        k === "forwarded"
                          ? "fill-blue-600"
                          : k === "blocked"
                            ? "fill-slate-500"
                            : k === "cache"
                              ? "fill-blue-300"
                              : k === "local"
                                ? "fill-sky-500"
                                : k === "error"
                                  ? "fill-indigo-800"
                                  : k === "rejected"
                                    ? "fill-violet-400"
                                    : "fill-slate-300"
                      }
                      x={0}
                      y={
                        100 -
                        (values[i]
                          .slice(0, j + 1)
                          .reduce((sum, value) => sum + value, 0) /
                          max) *
                          100
                      }
                      width={1}
                      height={(values[i][j] / max) * 100}
                    />
                  ))}
                </svg>
              )}
            </div>
          ))}
        </div>
      ) : (
        <p className="p-9 text-center text-muted-foreground">
          No traffic buckets in this range.
        </p>
      )}
      <details
        className="min-w-0 border-t border-border px-5 py-3 text-xs"
        onToggle={(e) => setTable(e.currentTarget.open)}
      >
        <summary className="cursor-pointer text-muted-foreground">
          View traffic as a table
        </summary>
        {table && (
          <DataTable
            items={buckets}
            columns={[
              {
                key: "time",
                label: "Interval",
                render: (r) => text(r.time),
              },
              ...outcomes.map((key) => ({
                key,
                label: labels[key],
                align: "right" as const,
                render: (r: Row) =>
                  r.gap || !r.outcomes
                    ? "Missing"
                    : count((r.outcomes as Row)[key]),
              })),
              { key: "complete", label: "Complete" },
            ]}
          />
        )}
      </details>
    </>
  );
}
