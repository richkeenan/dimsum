import { useId, useState } from "react";
import { count, type LatencyPoint } from "@/lib/api";
import { DataTable } from "@/components/data";
import { estimate, formatLatency, series } from "./format";

const dateLabel = (value: string) =>
  new Date(value).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });

export default function LatencyChart({
  points,
  compact = false,
}: {
  points: LatencyPoint[];
  compact?: boolean;
}) {
  const id = useId();
  const [active, setActive] = useState<number | null>(null);
  const [table, setTable] = useState(false);
  const maximum = Math.max(1, ...points.map((p) => (p.gap ? 0 : Number(p.p99_us ?? 0))));
  const ceiling =
    Math.ceil(maximum / 10 ** Math.floor(Math.log10(maximum))) *
    10 ** Math.floor(Math.log10(maximum));
  const times = points.map((point) => Date.parse(point.time));
  const span = (times.at(-1) ?? 0) - (times[0] ?? 0);
  const x = (i: number) => (span > 0 ? ((times[i] - times[0]) / span) * 1000 : 500);
  const y = (value: string) => 190 - (Number(value) / ceiling) * 180;
  const selected = active == null ? undefined : points[Math.min(active, points.length - 1)];
  const hasValues = points.some((p) => !p.gap && p.p99_us != null);
  return (
    <>
      <div className="flex flex-wrap items-center gap-x-5 gap-y-2 px-5 py-3 text-xs text-muted-foreground">
        {series.map((s) => (
          <span key={s.key} className="inline-flex items-center gap-2">
            <svg width="20" height="10" aria-hidden="true">
              <path d="M0 5H20" stroke={s.colour} strokeWidth="2" strokeDasharray={s.dash} />
            </svg>
            {s.label}
          </span>
        ))}
      </div>
      {hasValues ? (
        <div className="px-4 sm:px-5">
          <div className="flex gap-2">
            <span id={id + "-help"} className="sr-only">
              Use left and right arrow keys to inspect intervals, Home and End to jump, or view the
              timing data table.
            </span>
            <div
              aria-hidden="true"
              className="flex w-16 shrink-0 flex-col justify-between pb-2.5 pt-2.5 text-right text-[11px] tabular-nums text-muted-foreground"
            >
              {[ceiling, ceiling / 2, 0].map((n) => (
                <span key={n}>{formatLatency(String(Math.round(n)))}</span>
              ))}
            </div>
            <div
              role="group"
              aria-label="Interactive response-time chart"
              aria-describedby={id + "-help"}
              tabIndex={0}
              className={`relative min-w-0 flex-1 rounded-sm outline-offset-4 focus-visible:outline-2 focus-visible:outline-primary ${compact ? "h-32" : "h-56 sm:h-64"}`}
              onFocus={() => setActive((n) => n ?? 0)}
              onBlur={() => setActive(null)}
              onPointerMove={(event) => {
                const box = event.currentTarget.getBoundingClientRect();
                const target = ((event.clientX - box.left) / box.width) * 1000;
                let nearest = 0;
                for (let i = 1; i < points.length; i++) {
                  if (Math.abs(x(i) - target) < Math.abs(x(nearest) - target)) nearest = i;
                }
                setActive(nearest);
              }}
              onPointerLeave={(event) => {
                if (document.activeElement !== event.currentTarget) setActive(null);
              }}
              onKeyDown={(event) => {
                if (!["ArrowLeft", "ArrowRight", "Home", "End", "Escape"].includes(event.key))
                  return;
                event.preventDefault();
                setActive((n) =>
                  event.key === "Escape"
                    ? null
                    : event.key === "Home"
                      ? 0
                      : event.key === "End"
                        ? points.length - 1
                        : Math.max(
                            0,
                            Math.min(
                              points.length - 1,
                              (n ?? 0) + (event.key === "ArrowRight" ? 1 : -1),
                            ),
                          ),
                );
              }}
            >
              <svg
                viewBox="0 0 1000 200"
                preserveAspectRatio="none"
                className="h-full w-full overflow-visible"
                role="img"
                aria-labelledby={id}
              >
                <title id={id}>
                  Median, p95 and p99 response times. Missing or incomplete intervals break the
                  trend.
                </title>
                {[10, 100, 190].map((v) => (
                  <line
                    key={v}
                    x1="0"
                    x2="1000"
                    y1={v}
                    y2={v}
                    className="stroke-border"
                    vectorEffect="non-scaling-stroke"
                  />
                ))}
                {series.map((s) => {
                  let connected = false;
                  const d = points
                    .map((p, i) => {
                      const value = p[s.key];
                      if (p.gap || value == null || !p.complete) {
                        connected = false;
                        return "";
                      }
                      const command = connected ? "L" : "M";
                      connected = true;
                      return `${command}${x(i)},${y(value)}`;
                    })
                    .join(" ");
                  return (
                    <g key={s.key}>
                      <path
                        data-series={s.key}
                        d={d}
                        fill="none"
                        stroke={s.colour}
                        strokeWidth="2"
                        strokeDasharray={s.dash}
                        vectorEffect="non-scaling-stroke"
                      />
                      {points.map((p, i) =>
                        p[s.key] != null &&
                        !p.gap &&
                        (!p.complete ||
                          !points[i - 1]?.complete ||
                          !points[i + 1]?.complete ||
                          points[i - 1]?.[s.key] == null ||
                          points[i + 1]?.[s.key] == null) ? (
                          <svg key={i} x={x(i)} y={y(p[s.key]!)} overflow="visible">
                            <circle
                              r="3"
                              fill={p.complete ? s.colour : "var(--background)"}
                              stroke={s.colour}
                              strokeWidth="1.5"
                              vectorEffect="non-scaling-stroke"
                            />
                          </svg>
                        ) : null,
                      )}
                    </g>
                  );
                })}
                {active != null && (
                  <line
                    x1={x(Math.min(active, points.length - 1))}
                    x2={x(Math.min(active, points.length - 1))}
                    y1="0"
                    y2="200"
                    className="stroke-muted-foreground"
                    strokeDasharray="3 3"
                    vectorEffect="non-scaling-stroke"
                  />
                )}
              </svg>
            </div>
          </div>
          <div className="ml-18 flex justify-between gap-3 pt-2 text-[11px] text-muted-foreground">
            <span>{dateLabel(points[0].time)}</span>
            <span className="text-right">{dateLabel(points[points.length - 1].time)}</span>
          </div>
          <div role="status" className="min-h-14 py-3 text-xs text-muted-foreground tabular-nums">
            {selected ? (
              <span>
                {dateLabel(selected.time)}
                {selected.gap ? (
                  " — Missing interval"
                ) : (
                  <>
                    {" "}
                    · {count(selected.count)} queries
                    {!selected.complete && " · Partial coverage"} · Median{" "}
                    {estimate(selected.p50_us)} · p95 {estimate(selected.p95_us)} · p99{" "}
                    {estimate(selected.p99_us)}
                  </>
                )}
              </span>
            ) : (
              <span className="sr-only">
                Hover or use arrow keys for timings. Open circles show partial coverage.
              </span>
            )}
          </div>
        </div>
      ) : (
        <p className="px-5 py-9 text-center text-xs leading-relaxed text-muted-foreground">
          {points.some((p) => p.count !== "0")
            ? "Timing trends unavailable. Choose a more recent range."
            : "No timings in this range."}
        </p>
      )}
      <details
        className="border-t border-border text-xs"
        onToggle={(event) => setTable(event.currentTarget.open)}
      >
        <summary className="px-5 py-3 text-muted-foreground">View timing data</summary>
        {table && (
          <DataTable
            items={points}
            columns={[
              {
                key: "time",
                label: "Interval",
                render: (p) => dateLabel(String(p.time)),
              },
              {
                key: "count",
                label: "Queries",
                align: "right",
                render: (p) => (p.gap ? "—" : count(p.count)),
              },
              {
                key: "average_us",
                label: "Average",
                align: "right",
                render: (p) => formatLatency(p.average_us as string | null),
              },
              ...series.map((s) => ({
                key: s.key,
                label: s.label,
                align: "right" as const,
                render: (p: Record<string, unknown>) => estimate(p[s.key] as string | null),
              })),
              {
                key: "complete",
                label: "Coverage",
                render: (p) => (p.gap ? "Missing" : p.complete ? "Complete" : "Partial"),
              },
            ]}
          />
        )}
      </details>
    </>
  );
}
