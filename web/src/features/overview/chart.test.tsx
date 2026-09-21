import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import type { Point } from "@/lib/api";
import TrafficChart from "./chart";

const expectedColours = [
  ["Local answer", "bg-emerald-500", "fill-emerald-500"],
  ["Blocked", "bg-red-600", "fill-red-600"],
  ["Cached", "bg-teal-400", "fill-teal-400"],
  ["Cached (stale)", "bg-amber-400", "fill-amber-400"],
  ["Forwarded", "bg-blue-600", "fill-blue-600"],
  ["Failed", "bg-rose-900", "fill-rose-900"],
  ["Rejected", "bg-orange-500", "fill-orange-500"],
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

  const { container } = render(<TrafficChart buckets={[bucket]} />);
  const bars = [...container.querySelectorAll("rect")];

  expectedColours.forEach(([label, swatchClass, fillClass], index) => {
    expect(screen.getByText(label).querySelector("i")).toHaveClass(swatchClass);
    expect(bars[index]).toHaveClass(fillClass);
  });
});
