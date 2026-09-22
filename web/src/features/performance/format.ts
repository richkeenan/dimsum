// Durations are bounded uint32 microseconds, unlike unbounded query counters.
export function formatLatency(value: string | null | undefined): string {
  if (value == null || !/^\d+$/.test(value)) return "—";
  const micros = Number(value);
  if (!Number.isFinite(micros)) return "—";
  const [n, unit] =
    micros >= 1_000_000
      ? [micros / 1_000_000, "s"]
      : micros >= 1000
        ? [micros / 1000, "ms"]
        : [micros, "µs"];
  return `${n.toLocaleString(undefined, { maximumFractionDigits: n < 10 ? 2 : n < 100 ? 1 : 0 })} ${unit}`;
}

export function estimate(value: string | null | undefined): string {
  return value == null ? "—" : `≈ ${formatLatency(value)}`;
}

export const outcomeLabels: Record<string, string> = {
  local: "Local answers",
  blocked: "Blocked",
  cache: "Cached",
  stale: "Cached (stale)",
  forwarded: "Forwarded",
  error: "Failed",
};

export const series = [
  { key: "p50_us", label: "Median", colour: "#0f8b82", dash: undefined },
  { key: "p95_us", label: "p95", colour: "#3576db", dash: "6 3" },
  { key: "p99_us", label: "p99", colour: "#c17b17", dash: "2 3" },
] as const;
