import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import type { Point } from "@/lib/api";
import TrafficChart from "./chart";

const expectedColours = [
  ["Local answer", "var(--color-emerald-500)"],
  ["Blocked", "var(--color-red-600)"],
  ["Cached", "var(--color-teal-400)"],
  ["Cached (stale)", "var(--color-amber-400)"],
  ["Forwarded", "var(--color-blue-600)"],
  ["Failed", "var(--color-rose-900)"],
  ["Rejected", "var(--color-orange-500)"],
] as const;

it("uses a distinct semantic colour for every outcome in the legend and graph", () => {
  const bucket = {
    time: "2026-09-21T12:00:00Z",
    complete: true,
    gap: false,
    duration_us: null,
    histogram: null,
    outcomes: {
      local: "1",
      blocked: "1",
      cache: "1",
      stale: "1",
      forwarded: "1",
      error: "1",
      rejected: "1",
    },
  } satisfies Point;

  const { container } = render(<TrafficChart buckets={[bucket]} resolution={3600} />);
  const bars = [...container.querySelectorAll('rect[data-ts-key^="queries:"]')];

  expectedColours.forEach(([label, colour], index) => {
    expect(screen.getByText(label).querySelector("i")).toHaveStyle({ background: colour });
    expect(bars[index]).toHaveAttribute("fill", colour);
  });
});
