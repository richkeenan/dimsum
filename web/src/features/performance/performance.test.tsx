import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Latency, LatencyPoint } from "@/lib/api";
import { formatLatency } from "./format";
import { LatencySummary, PrecisionNotice } from "./summary";
import LatencyChart from "./chart";

const metrics: Latency = {
  count: "100",
  average_us: "1500",
  p50_us: "0",
  p95_us: "2000",
  p99_us: "100000",
  percentiles_available: true,
};
const point = (
  time: string,
  extra: Partial<LatencyPoint> = {},
): LatencyPoint => ({
  ...metrics,
  time,
  complete: true,
  gap: false,
  ...extra,
});

describe("response-time presentation", () => {
  it("distinguishes measured zero from absent timing and uses readable units", () => {
    expect(formatLatency("0")).toBe("0 µs");
    expect(formatLatency(null)).toBe("—");
    expect(formatLatency("125")).toBe("125 µs");
    expect(formatLatency("1250")).toBe("1.25 ms");
    expect(formatLatency("1500000")).toBe("1.5 s");
    render(<LatencySummary metrics={metrics} />);
    expect(screen.getByText("≈ 0 µs")).toBeInTheDocument();
    expect(screen.getByText("≈ 100 ms")).toBeInTheDocument();
  });

  it("explains missing legacy precision without converting it to zero", () => {
    render(
      <PrecisionNotice
        metrics={{
          ...metrics,
          percentiles_available: false,
          p50_us: null,
          p95_us: null,
          p99_us: null,
        }}
      />,
    );
    expect(screen.getByText(/older history/i)).toBeInTheDocument();
  });

  it("breaks trends across missing and incomplete intervals and exposes keyboard values", () => {
    const points = [
      point("2026-09-22T10:00:00Z"),
      point("2026-09-22T10:01:00Z", {
        gap: true,
        complete: false,
        count: "0",
        p50_us: null,
        p95_us: null,
        p99_us: null,
        average_us: null,
        percentiles_available: false,
      }),
      point("2026-09-22T10:02:00Z"),
      point("2026-09-22T10:03:00Z", { complete: false }),
      point("2026-09-22T10:04:00Z"),
    ];
    const { container } = render(<LatencyChart points={points} />);
    const line = container.querySelector('path[data-series="p95_us"]');
    expect(line?.getAttribute("d")?.match(/M/g)).toHaveLength(3);
    const plot = screen.getByRole("group", {
      name: /interactive response-time chart/i,
    });
    fireEvent.focus(plot);
    fireEvent.keyDown(plot, { key: "ArrowRight" });
    expect(screen.getByRole("status")).toHaveTextContent(/missing interval/i);
    fireEvent.keyDown(plot, { key: "ArrowRight" });
    expect(screen.getByRole("status")).toHaveTextContent("p95 ≈ 2 ms");
    fireEvent.click(screen.getByText("View timing data"));
    // Native details toggle is not simulated by jsdom; fire its state event.
    const details = container.querySelector("details")!;
    details.open = true;
    fireEvent(details, new Event("toggle"));
    expect(
      within(screen.getByRole("table")).getByText("Missing"),
    ).toBeInTheDocument();
  });
});
