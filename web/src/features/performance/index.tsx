import { DataTable, Resource } from "@/components/data";
import { count, type Performance as PerformanceData } from "@/lib/api";
import { useResource } from "@/lib/hooks";
import LatencyChart from "./chart";
import Distribution from "./distribution";
import { LatencySummary, PrecisionNotice } from "./summary";
import { estimate, formatLatency, outcomeLabels } from "./format";

export type PerformanceProps = {
  range: string;
  resolution: number;
  refresh: number;
};

export function usePerformance({ range, resolution, refresh }: PerformanceProps) {
  return useResource<PerformanceData>(
    `performance?${range}&resolution_seconds=${resolution}`,
    refresh,
  );
}

const panel = "mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background";
const heading =
  "flex flex-wrap items-center justify-between gap-2 border-b border-border px-5 py-3";

export default function Performance(props: PerformanceProps) {
  const state = usePerformance(props);
  const data = state.data;
  return (
    <Resource state={state}>
      {data && (
        <>
          <section className={panel} aria-label="Response-time summary">
            <div className={heading}>
              <h2 className="text-sm font-medium">Server-side response time</h2>
              <span className="text-xs text-muted-foreground">
                {count(data.summary.count)} queries observed
                {data.complete === false && " · Partial history"}
              </span>
            </div>
            <LatencySummary metrics={data.summary} />
            <PrecisionNotice metrics={data.summary} />
          </section>
          <section className={panel}>
            <div className={heading}>
              <h2 className="text-sm font-medium">Response time over time</h2>
              <span className="text-xs text-muted-foreground">
                {data.resolution_seconds === 60
                  ? "Minute"
                  : data.resolution_seconds === 3600
                    ? "Hourly"
                    : "Daily"}{" "}
                intervals
              </span>
            </div>
            <LatencyChart points={data.points} />
          </section>
          <div className="grid min-w-0 gap-x-5 min-[1400px]:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
            <section className={panel}>
              <div className={heading}>
                <h2 className="text-sm font-medium">Response-time distribution</h2>
              </div>
              <Distribution bands={data.distribution} total={data.summary.count} />
            </section>
            <section className={panel}>
              <div className={heading}>
                <h2 className="text-sm font-medium">By query outcome</h2>
              </div>
              <DataTable
                items={data.outcomes}
                columns={[
                  {
                    key: "outcome",
                    label: "Outcome",
                    render: (r) => outcomeLabels[String(r.outcome)] ?? String(r.outcome),
                  },
                  {
                    key: "count",
                    label: "Queries",
                    align: "right",
                    render: (r) => count(r.count),
                  },
                  {
                    key: "average_us",
                    label: "Average",
                    align: "right",
                    render: (r) => formatLatency(r.average_us as string | null),
                  },
                  {
                    key: "p95_us",
                    label: "p95",
                    align: "right",
                    render: (r) => estimate(r.p95_us as string | null),
                  },
                  {
                    key: "p99_us",
                    label: "p99",
                    align: "right",
                    render: (r) => estimate(r.p99_us as string | null),
                  },
                ]}
              />
            </section>
          </div>
          <details className="text-xs text-muted-foreground">
            <summary className="cursor-pointer">Measurement details</summary>
            <p className="mt-2 max-w-4xl leading-relaxed">
              Server-side timings include upstream waits and exclude admission rejections. ≈ marks
              histogram estimates (up to 3.125% rounding error).
            </p>
          </details>
        </>
      )}
    </Resource>
  );
}

export function OverviewPerformance(props: PerformanceProps & { onOpen: () => void }) {
  const state = usePerformance(props);
  const metrics = state.data?.summary;
  const cards = [
    ["Average response", formatLatency(metrics?.average_us)],
    ["p95 response", estimate(metrics?.p95_us)],
    ["p99 response", estimate(metrics?.p99_us)],
  ];
  return (
    <section className="mb-5" aria-label="Response time">
      <Resource state={state}>
        <div className="grid grid-cols-3 gap-3">
          {cards.map(([label, value]) => (
            <button
              key={label}
              onClick={props.onOpen}
              aria-label={`View performance: ${label}, ${value}`}
              title={`View response-time details${state.data?.complete === false ? " (partial history coverage)" : ""}`}
              className="min-w-0 rounded-lg border border-border bg-background px-3 py-3 text-left hover:border-primary/50 hover:bg-accent/30 sm:px-5"
            >
              <span className="block text-xs text-muted-foreground">{label}</span>
              <strong className="mt-1 block whitespace-nowrap text-lg font-[550] tracking-tight tabular-nums sm:text-[25px]">
                {value}
              </strong>
            </button>
          ))}
        </div>
      </Resource>
    </section>
  );
}
