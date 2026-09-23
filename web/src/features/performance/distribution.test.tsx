import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it } from "vitest";
import Distribution from "./distribution";

it("sorts response bands by bounds or exact counts rather than rounded shares", () => {
  render(
    <Distribution
      total="100"
      bands={[
        { lower_us: "1000", upper_us: "10000", count: "60" },
        { lower_us: "10000", upper_us: null, count: "30" },
        { lower_us: "0", upper_us: "1000", count: "10" },
      ]}
    />,
  );
  const labels = () =>
    screen
      .getAllByRole("row")
      .slice(1)
      .map((r) => within(r).getByRole("rowheader").textContent);
  expect(labels()).toEqual(["< 1 ms", "1 ms – < 10 ms", "≥ 10 ms"]);
  fireEvent.click(screen.getByRole("button", { name: "Share" }));
  expect(labels()).toEqual(["1 ms – < 10 ms", "≥ 10 ms", "< 1 ms"]);
  fireEvent.click(screen.getByRole("button", { name: "Queries" }));
  fireEvent.click(screen.getByRole("button", { name: "Queries" }));
  expect(labels()).toEqual(["< 1 ms", "≥ 10 ms", "1 ms – < 10 ms"]);
});
