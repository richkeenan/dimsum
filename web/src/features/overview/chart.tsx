import { useId, useMemo, useState } from "react";
import { bandX, barY, defineChart, stack } from "@tanstack/charts";
import { Chart } from "@tanstack/charts/react";
import { scaleBand } from "@tanstack/charts/scales/band";
import { scaleLinear } from "@tanstack/charts/scales/linear";
import { tooltip } from "@tanstack/charts/tooltip";
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
// Stable frequency-oriented presentation; never reorder as live counts change.
const legendOrder = [
  "forwarded",
  "stale",
  "cache",
  "blocked",
  "local",
  "error",
  "rejected",
] as const;
const colours = {
  local: "var(--color-emerald-500)",
  blocked: "var(--color-red-600)",
  cache: "var(--color-teal-400)",
  stale: "var(--color-amber-400)",
  forwarded: "var(--color-blue-600)",
  error: "var(--color-rose-900)",
  rejected: "var(--color-orange-500)",
};
type TrafficDatum = { time: string; outcome: (typeof outcomes)[number]; value: number };

export default function TrafficChart({
  buckets,
  resolution,
}: {
  buckets: Point[];
  resolution: number;
}) {
  const [table, setTable] = useState(false);
  const patternId = useId().replace(/:/g, "") + "-coverage";
  const interval =
    resolution >= 86400
      ? `${resolution / 86400}-day`
      : resolution >= 3600
        ? `${resolution / 3600}-hour`
        : `${resolution / 60}-minute`;
  const definition = useMemo(() => {
    const rows: TrafficDatum[] = buckets.flatMap((b) =>
      b.gap || !b.outcomes
        ? []
        : outcomes.map((outcome) => ({
            time: b.time,
            outcome,
            value: Math.max(0, Number(b.outcomes![outcome]) || 0),
          })),
    );
    const coverage: TrafficDatum[] = buckets
      .filter((b) => b.gap || !b.outcomes || !b.complete)
      .map((b) => ({ time: b.time, outcome: "local", value: 0 }));
    const byTime = new Map(buckets.map((b) => [b.time, b]));
    const maximum = Math.max(
      1,
      ...buckets.map((b) =>
        b.gap || !b.outcomes ? 0 : outcomes.reduce((sum, k) => sum + Number(b.outcomes![k]), 0),
      ),
    );
    const multiDay =
      buckets.length > 1 &&
      new Date(buckets[0].time).toDateString() !== new Date(buckets.at(-1)!.time).toDateString();
    const timeFormat = new Intl.DateTimeFormat(undefined, {
      ...(multiDay ? ({ month: "short", day: "numeric" } as const) : {}),
      hour: "2-digit",
      minute: "2-digit",
    });
    const numberFormat = new Intl.NumberFormat(undefined, {
      notation: "compact",
      maximumFractionDigits: 1,
    });
    return defineChart({
      chart: ({ width }) => ({
        marks: [
          bandX(coverage, { id: "coverage", x: "time", fill: `url(#${patternId})` }),
          barY(rows, {
            id: "queries",
            x: "time",
            y: "value",
            z: "outcome",
            fill: (d) => colours[d.outcome],
            layout: stack({ order: outcomes }),
          }),
        ],
        scales: {
          x: {
            scale: scaleBand<string>()
              .domain(buckets.map((b) => b.time))
              .padding(0.08),
            axis: {
              label: "Time (local)",
              ticks: {
                count: width < 520 ? 3 : 6,
                format: (value: string) => timeFormat.format(new Date(value)),
              },
            },
          },
          y: {
            scale: scaleLinear().domain([0, maximum]),
            nice: true,
            grid: true,
            axis: {
              label: "Queries per interval",
              ticks: {
                count: 4,
                format: (value: number) =>
                  Number.isInteger(value) ? numberFormat.format(value) : "",
              },
            },
          },
        },
        theme: {
          foreground: "var(--muted-foreground)",
          muted: "var(--muted-foreground)",
          grid: "var(--border)",
          background: "transparent",
        },
      }),
      focus: "group-x",
      tooltip: {
        use: tooltip,
        formatGroup(points: readonly { datum: TrafficDatum }[]) {
          const bucket = byTime.get(points[0]?.datum.time ?? "");
          if (!bucket) return "";
          const heading = new Date(bucket.time).toLocaleString();
          if (bucket.gap || !bucket.outcomes) return `${heading}\nMissing interval`;
          return [
            heading,
            ...(!bucket.complete ? ["Partial coverage"] : []),
            ...legendOrder.map((key) => `${labels[key]}: ${count(bucket.outcomes![key])}`),
          ].join("\n");
        },
      },
    });
  }, [buckets, patternId]);
  return (
    <>
      <div className="flex flex-wrap items-center gap-x-[18px] gap-y-2.5 px-5 py-[18px] text-xs text-muted-foreground">
        {legendOrder.map((k) => (
          <span className="flex items-center gap-[5px]" key={k}>
            <i className="size-2 rounded-[2px]" style={{ background: colours[k] }} />
            {labels[k]}
          </span>
        ))}
        <span className="flex items-center gap-[5px]">
          <i className="size-2 rounded-[2px] bg-[repeating-linear-gradient(135deg,transparent,transparent_4px,var(--border)_4px,var(--border)_5px)]" />
          Missing / partial interval
        </span>
      </div>
      {buckets.length ? (
        <div className="px-3 pb-2 sm:px-5">
          <p className="mb-2 text-xs text-muted-foreground">
            {interval} intervals · hover or use arrow keys for exact counts
          </p>
          <svg className="absolute size-0" aria-hidden="true">
            <defs>
              <pattern id={patternId} patternUnits="userSpaceOnUse" width="7" height="7">
                <path d="M-1 1L1 -1M0 7L7 0M6 8L8 6" stroke="var(--border)" strokeWidth="1" />
              </pattern>
            </defs>
          </svg>
          <Chart
            definition={definition}
            height={300}
            ariaLabel="Query activity: DNS outcomes over time"
          />
        </div>
      ) : (
        <p className="p-9 text-center text-muted-foreground">No traffic buckets in this range.</p>
      )}
      <details
        className="min-w-0 border-t border-border px-5 py-3 text-xs"
        onToggle={(e) => setTable(e.currentTarget.open)}
      >
        <summary className="cursor-pointer text-muted-foreground">View traffic as a table</summary>
        {table && (
          <DataTable
            items={buckets}
            initialSorting={[{ id: "time", desc: false }]}
            columns={[
              { key: "time", label: "Interval", sortType: "datetime", render: (r) => text(r.time) },
              ...legendOrder.map((key) => ({
                key,
                label: labels[key],
                sortType: "number" as const,
                sortValue: (r: Row) => (r.gap ? undefined : (r.outcomes as Row | undefined)?.[key]),
                align: "right" as const,
                render: (r: Row) =>
                  r.gap || !r.outcomes ? "Missing" : count((r.outcomes as Row)[key]),
              })),
              { key: "complete", label: "Complete" },
            ]}
          />
        )}
      </details>
    </>
  );
}
