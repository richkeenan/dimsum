import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it } from "vitest";
import { QueryHistoryStatus } from "./history";

const writer = {
  LastSuccess: "2026-09-23T12:00:00Z",
  LastError: "",
  LostDetails: "0",
  Backlogged: false,
};

it("requires a successful write before claiming history has been saved", () => {
  const view = render(
    <QueryHistoryStatus
      storage={{ available: true, writer: { ...writer, LastSuccess: "0001-01-01T00:00:00Z" } }}
    />,
  );
  expect(screen.getByText(/Waiting for query history/)).toBeVisible();
  expect(screen.queryByText(/Last saved/)).not.toBeInTheDocument();
  view.rerender(<QueryHistoryStatus storage={{ available: true, writer }} />);
  expect(screen.getByText(/Last saved/)).toBeVisible();
  expect(screen.queryByText(/Waiting for query history/)).not.toBeInTheDocument();
});

it("explains a write failure with a concrete next step and hides the raw error", async () => {
  const error = "database or disk is full (13)";
  const view = render(
    <QueryHistoryStatus storage={{ available: true, writer: { ...writer, LastError: error } }} />,
  );
  expect(screen.getByText(/Recent queries may be missing/)).toBeVisible();
  expect(screen.getByText(/Free up disk space/)).toBeVisible();
  expect(screen.queryByText(error)).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Query history details" }));
  expect(screen.getByRole("dialog")).toHaveTextContent(error);
  await userEvent.keyboard("{Escape}");
  view.rerender(
    <QueryHistoryStatus storage={{ available: true, writer: { ...writer, LostDetails: "7" } }} />,
  );
  expect(screen.queryByText(/Recent queries may be missing/)).not.toBeInTheDocument();
  expect(screen.getByText(/7 query details could not be saved earlier/)).toBeVisible();
});

it("does not confuse cleanup backlog or maintenance failures with lost query writes", async () => {
  const view = render(
    <QueryHistoryStatus storage={{ available: true, writer: { ...writer, Backlogged: true } }} />,
  );
  expect(screen.queryByText(/Recent queries may be missing/)).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Query history details" }));
  expect(screen.getByRole("dialog")).toHaveTextContent(/Old-history cleanup is catching up/);
  await userEvent.keyboard("{Escape}");
  view.rerender(
    <QueryHistoryStatus
      storage={{
        available: true,
        writer: { ...writer, LastError: "retention: database is locked" },
      }}
    />,
  );
  expect(screen.getByText(/History maintenance needs attention/)).toBeVisible();
  expect(screen.queryByText(/Recent queries may be missing/)).not.toBeInTheDocument();
});

it("does not show startup failures as working storage or reveal raw errors", () => {
  render(
    <QueryHistoryStatus
      storage={{ available: false, error: "open /state/history.db: permission denied" }}
    />,
  );
  expect(screen.getByText("Query history is unavailable")).toBeVisible();
  expect(screen.getByText(/permission to write/)).toBeVisible();
  expect(screen.getByText(/restart Dimsum/)).toBeVisible();
  expect(screen.queryByText(/open \/state/)).not.toBeInTheDocument();
});
