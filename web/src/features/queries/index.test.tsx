import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import Queries from "./index";

const mocks = vi.hoisted(() => ({ resource: vi.fn(), live: vi.fn() }));
vi.mock("@/lib/hooks", () => ({
  useResource: mocks.resource,
  useLive: mocks.live,
}));
const row = {
  id: "9007199254740999",
  name: "example.test",
  client: "192.0.2.1",
  time: "2026-09-21T13:24:56Z",
  outcome: "blocked",
  qtype: "A",
  rule_id: "9007199254740997",
  generation: "9007199254740995",
  boot_id: "boot-a",
  duration_us: "1234",
};
beforeEach(() => {
  mocks.resource.mockReset();
  mocks.live.mockReset().mockReturnValue("Live");
  mocks.resource.mockImplementation((path: string) => ({
    data: path.startsWith("queries/")
      ? row
      : { items: [row], next_cursor: "cursor-a", complete: true },
    loading: false,
    isFetching: false,
  }));
});

it("syncs URL filters without persisting them again, and persists user apply and clear", () => {
  const onFilterChange = vi.fn();
  const props = {
    range: "from=one&to=two",
    refresh: 0,
    onLiveTick: vi.fn(),
    onFilterChange,
  };
  const view = render(
    <Queries {...props} initialFilter={{ name: "first.test" }} />,
  );
  view.rerender(<Queries {...props} initialFilter={{ name: "second.test" }} />);
  expect(screen.getByLabelText("Filter name")).toHaveValue("second.test");
  expect(onFilterChange).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("Filter name"), {
    target: { value: "third.test" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Apply filters" }));
  expect(onFilterChange).toHaveBeenLastCalledWith({ name: "third.test" });
  fireEvent.click(screen.getByRole("button", { name: "Clear" }));
  expect(onFilterChange).toHaveBeenLastCalledWith({});
});

it("captures cursor range and pauses polling until returning to the newest page", () => {
  const props = { initialFilter: {}, refresh: 0, onLiveTick: vi.fn() };
  const view = render(<Queries {...props} range="from=one&to=two" />);
  expect(mocks.live).toHaveBeenLastCalledWith(true, expect.any(Function));
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  view.rerender(<Queries {...props} range="from=three&to=four" />);
  expect(mocks.resource).toHaveBeenLastCalledWith(
    "queries?from=one&to=two&limit=100&cursor=cursor-a",
    0,
  );
  expect(mocks.live).toHaveBeenLastCalledWith(false, expect.any(Function));
  fireEvent.click(screen.getByRole("button", { name: "Previous" }));
  expect(mocks.resource).toHaveBeenLastCalledWith(
    "queries?from=three&to=four&limit=100",
    0,
  );
  expect(mocks.live).toHaveBeenLastCalledWith(true, expect.any(Function));
});

it("keeps scoped identifiers exact when filtering from query details", () => {
  const onFilterChange = vi.fn();
  render(
    <Queries
      range="from=one&to=two"
      initialFilter={{}}
      refresh={0}
      onLiveTick={vi.fn()}
      onFilterChange={onFilterChange}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "example.test" }));
  expect(mocks.live).toHaveBeenLastCalledWith(false, expect.any(Function));
  fireEvent.click(
    screen.getByRole("button", { name: "Queries for this rule" }),
  );
  expect(onFilterChange).toHaveBeenLastCalledWith({
    rule_id: row.rule_id,
    generation: row.generation,
    boot_id: row.boot_id,
  });
});

it("does not poll or advance fixed historical ranges", () => {
  const onLiveTick = vi.fn();
  render(
    <Queries
      range="from=one&to=two"
      initialFilter={{}}
      refresh={0}
      onLiveTick={onLiveTick}
      liveAllowed={false}
    />,
  );
  expect(mocks.live).toHaveBeenLastCalledWith(false, expect.any(Function));
  expect(screen.getByRole("button", { name: "Pause live" })).toBeDisabled();
  expect(onLiveTick).not.toHaveBeenCalled();
});
