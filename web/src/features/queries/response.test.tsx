import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { ResponseRecords, ResponseTime } from "./response";

it("distinguishes unavailable data, negative answers and partial capture", () => {
  const view = render(<ResponseRecords />);
  expect(screen.getByText(/wasn’t recorded/)).toBeInTheDocument();
  view.rerender(
    <ResponseRecords response={{ records: [], truncated: false }} />,
  );
  expect(screen.getByText("No answer records returned.")).toBeInTheDocument();
  expect(screen.queryByText(/wasn’t recorded/)).not.toBeInTheDocument();
  view.rerender(
    <ResponseRecords response={{ records: [], truncated: true }} />,
  );
  expect(
    screen.getByText(/No answer records in the captured portion/),
  ).toBeInTheDocument();
  expect(screen.getByText(/Partial response:/)).toBeInTheDocument();
});

it("keeps fast timings nonzero and full precision available", () => {
  const view = render(<ResponseTime value="195" />);
  expect(screen.getByTitle("0.195 ms")).toHaveTextContent("0.20 ms");
  view.rerender(<ResponseTime value="1" />);
  expect(screen.getByTitle("0.001 ms")).toHaveTextContent("<0.01 ms");
  view.rerender(<ResponseTime value="1234567" />);
  expect(screen.getByTitle("1,234.567 ms")).toHaveTextContent("1.23 s");
  view.rerender(<ResponseTime value={undefined} />);
  expect(screen.getByText("—")).toBeInTheDocument();
});
