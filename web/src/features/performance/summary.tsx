import type { Latency } from "@/lib/api";
import { estimate, formatLatency } from "./format";

export function LatencySummary({
  metrics,
  compact = false,
}: {
  metrics?: Latency;
  compact?: boolean;
}) {
  const items = [
    {
      label: "Average",
      value: formatLatency(metrics?.average_us),
      hint: "Mean resolution time",
    },
    ...(!compact
      ? [
          {
            label: "Median · p50",
            value: estimate(metrics?.p50_us),
            hint: "Half of queries finish within",
          },
        ]
      : []),
    {
      label: "p95",
      value: estimate(metrics?.p95_us),
      hint: "95% of queries finish within",
    },
    {
      label: "p99",
      value: estimate(metrics?.p99_us),
      hint: "99% of queries finish within",
    },
  ];
  return (
    <dl
      className={`grid ${compact ? "grid-cols-3" : "grid-cols-2 md:grid-cols-4"}`}
    >
      {items.map(({ label, value, hint }, index) => (
        <div
          key={label}
          className={`min-w-0 px-3 py-4 sm:px-5 ${index ? "border-l border-border" : ""} ${!compact && index === 2 ? "max-md:border-l-0" : ""} ${!compact && index > 1 ? "max-md:border-t max-md:border-border" : ""}`}
        >
          <dt className="text-xs text-muted-foreground" title={hint}>
            {label}
          </dt>
          <dd
            className={`mt-1 whitespace-nowrap font-[550] tracking-tight tabular-nums ${compact ? "text-lg sm:text-2xl" : "text-2xl min-[1101px]:text-[30px]"}`}
          >
            {value}
          </dd>
        </div>
      ))}
    </dl>
  );
}

export function PrecisionNotice({ metrics }: { metrics?: Latency }) {
  if (!metrics || metrics.count === "0" || metrics.percentiles_available)
    return null;
  return (
    <p className="px-5 pb-4 text-xs leading-relaxed text-muted-foreground">
      Percentiles unavailable for older history. Choose a more recent range.
    </p>
  );
}
