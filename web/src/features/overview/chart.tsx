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
      <div className="legend">
        {outcomes.map((k) => (
          <span key={k}>
            <i className={k} />
            {labels[k]}
          </span>
        ))}
        <span>
          <i className="gap" />
          Missing interval
        </span>
      </div>
      {buckets.length ? (
        <div
          className="traffic-chart"
          style={{ gap: buckets.length > 100 ? 0 : 3 }}
          role="img"
          aria-label="DNS outcomes over time. Exact values are available in the traffic table below."
        >
          {buckets.map((b, i) => (
            <div
              className={
                "bar " +
                (b.gap || !b.outcomes || b.complete === false ? "gap" : "")
              }
              key={i}
              title={`${b.time}: ${b.gap || !b.outcomes ? "Missing interval" : (b.complete === false ? "Partial coverage. " : "") + outcomes.map((k) => `${labels[k]}: ${count(b.outcomes?.[k])}`).join(", ")}`}
            >
              {!b.gap &&
                b.outcomes &&
                outcomes.map((k, j) => (
                  <div
                    key={k}
                    className={k}
                    style={{ height: `${(values[i][j] / max) * 100}%` }}
                  />
                ))}
            </div>
          ))}
        </div>
      ) : (
        <p className="empty">No traffic buckets in this range.</p>
      )}
      <details onToggle={(e) => setTable(e.currentTarget.open)}>
        <summary>View traffic as a table</summary>
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
