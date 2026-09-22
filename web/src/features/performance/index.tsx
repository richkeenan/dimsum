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

export function usePerformance({
  range,
  resolution,
  refresh,
}: PerformanceProps) {
  return useResource<PerformanceData>(
    `performance?${range}&resolution_seconds=${resolution}`,
    refresh,
  );
}

const panel =
  "mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background";
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
              </span>
            </div>
            <LatencySummary metrics={data.summary} />
            <PrecisionNotice metrics={data.summary} />
          </section>
          <CoverageNotice complete={data.complete} />
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
                <h2 className="text-sm font-medium">
                  Response-time distribution
                </h2>
              </div>
              <Distribution
                bands={data.distribution}
                total={data.summary.count}
              />
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
                    render: (r) =>
                      outcomeLabels[String(r.outcome)] ?? String(r.outcome),
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
              <p className="px-5 py-4 text-xs leading-relaxed text-muted-foreground">
                Cached and local answers show the fast path. Forwarded queries
                include the wait for upstream DNS. Failed resolutions are
                included in the totals.
              </p>
            </section>
          </div>
          <p className="max-w-4xl text-xs leading-relaxed text-muted-foreground">
            Timings measure resolution inside dimsum, including upstream waits,
            rather than the network round trip from your device. Percentiles
            marked ≈ are histogram estimates with up to 3.125% rounding error.
            Admission rejections are excluded.
          </p>
        </>
      )}
    </Resource>
  );
}

export function CoverageNotice({ complete }: { complete?: boolean }) {
  return complete === false ? (
    <p className="mb-4 text-xs leading-relaxed text-muted-foreground">
      Partial coverage: figures reflect recorded queries. Some intervals are
      still being collected, missing, or outside history retention.
    </p>
  ) : null;
}

export function OverviewPerformance(
  props: PerformanceProps & { onOpen: () => void },
) {
  const state = usePerformance(props);
  return (
    <section className={panel}>
      <div className={heading}>
        <h2 className="text-sm font-medium">Response time</h2>
        <button
          onClick={props.onOpen}
          className="min-h-8 text-xs text-primary hover:underline"
        >
          View performance
        </button>
      </div>
      <Resource state={state}>
        {state.data && (
          <>
            <LatencySummary metrics={state.data.summary} compact />
            <PrecisionNotice metrics={state.data.summary} />
            <div className="px-5">
              <CoverageNotice complete={state.data.complete} />
            </div>
            <LatencyChart points={state.data.points} compact />
            <p className="border-t border-border px-5 py-3 text-xs text-muted-foreground">
              Server-side resolution, including upstream waits. Percentiles are
              approximate.
            </p>
          </>
        )}
      </Resource>
    </section>
  );
}
