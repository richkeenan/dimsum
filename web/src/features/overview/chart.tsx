import { count, text, type Row } from "@/lib/api";
import { DataTable } from "@/components/data";
const outcomes = ["forwarded", "blocked", "cached", "stale", "errors"];
export default function TrafficChart({ buckets }: { buckets: Row[] }) {
  const values = buckets.map((b) =>
    outcomes.map((k) => {
      const n = Number(b[k]);
      return Number.isFinite(n) && n >= 0 ? n : 0;
    }),
  );
  const max = Math.max(1, ...values.map((v) => v.reduce((a, b) => a + b, 0)));
  return (
    <>
      <div className="legend">
        {outcomes.map((k) => (
          <span key={k}>
            <i className={k} />
            {k}
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
          role="img"
          aria-label="DNS outcomes over time. Exact values are available in the traffic table below."
        >
          {buckets.map((b, i) => (
            <div
              className={
                "bar " + (b.missing || b.complete === false ? "gap" : "")
              }
              key={i}
              title={`${text(b.time ?? b.start)}: ${b.missing ? "missing" : values[i].reduce((a, v) => a + v, 0) + " queries"}`}
            >
              {!b.missing &&
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
      <details>
        <summary>View traffic as a table</summary>
        <DataTable
          items={buckets}
          columns={[
            {
              key: "time",
              label: "Interval",
              render: (r) => text(r.time ?? r.start),
            },
            ...outcomes.map((key) => ({
              key,
              label: key,
              render: (r: Row) => (r.missing ? "Missing" : count(r[key])),
            })),
            { key: "complete", label: "Complete" },
          ]}
        />
      </details>
    </>
  );
}
