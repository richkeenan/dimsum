import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
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
  vi.useFakeTimers();
  mocks.resource.mockReset();
  mocks.live.mockReset().mockReturnValue("Live");
  mocks.resource.mockImplementation((path: string) => ({
    data: path.startsWith("clients?")
      ? { items: [{ address: "192.0.2.1", name: "Study laptop" }] }
      : path.startsWith("queries/")
        ? row
        : { items: [row], next_cursor: "cursor-a", complete: true },
    loading: false,
    isFetching: false,
  }));
});
afterEach(() => vi.useRealTimers());

it("syncs URL filters and debounces edits, cancelling pending edits on clear", () => {
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
  expect(onFilterChange).not.toHaveBeenCalled();
  act(() => vi.advanceTimersByTime(350));
  expect(onFilterChange).toHaveBeenLastCalledWith({ name: "third.test" });
  fireEvent.change(screen.getByLabelText("Filter name"), {
    target: { value: "pending.test" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Clear" }));
  act(() => vi.advanceTimersByTime(350));
  expect(onFilterChange).toHaveBeenLastCalledWith({});
});

it("selects a client by name with the keyboard and immediately resets pagination", () => {
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
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  const client = screen.getByRole("combobox", { name: "Filter client" });
  fireEvent.change(client, { target: { value: "study" } });
  expect(
    screen.getByRole("option", { name: /Study laptop.*192.0.2.1/ }),
  ).toBeInTheDocument();
  fireEvent.keyDown(client, { key: "ArrowDown" });
  fireEvent.keyDown(client, { key: "Enter" });
  expect(onFilterChange).toHaveBeenLastCalledWith({ client: "192.0.2.1" });
  expect(screen.getByText("Page 1 · up to 100 queries")).toBeInTheDocument();
});

it("waits for scope before sending an advanced identity filter", () => {
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
  fireEvent.change(screen.getByLabelText("Filter rule_id"), {
    target: { value: "9" },
  });
  act(() => vi.advanceTimersByTime(350));
  expect(onFilterChange).not.toHaveBeenCalled();
  expect(screen.getByText(/Add a boot ID and generation/)).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Filter boot_id"), {
    target: { value: "boot-a" },
  });
  fireEvent.change(screen.getByLabelText("Filter generation"), {
    target: { value: "42" },
  });
  act(() => vi.advanceTimersByTime(350));
  expect(onFilterChange).toHaveBeenLastCalledWith({
    rule_id: "9",
    boot_id: "boot-a",
    generation: "42",
  });
});

it("captures cursor range and pauses polling until returning to the newest page", () => {
  const props = { initialFilter: {}, refresh: 0, onLiveTick: vi.fn() };
  const view = render(<Queries {...props} range="from=one&to=two" />);
  expect(mocks.live).toHaveBeenLastCalledWith(true, expect.any(Function));
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  view.rerender(<Queries {...props} range="from=three&to=four" />);
  expect(
    mocks.resource.mock.calls
      .filter(([path]) => path.startsWith("queries?"))
      .at(-1),
  ).toEqual(["queries?from=one&to=two&limit=100&cursor=cursor-a", 0]);
  expect(mocks.live).toHaveBeenLastCalledWith(false, expect.any(Function));
  fireEvent.click(screen.getByRole("button", { name: "Previous" }));
  expect(
    mocks.resource.mock.calls
      .filter(([path]) => path.startsWith("queries?"))
      .at(-1),
  ).toEqual(["queries?from=three&to=four&limit=100", 0]);
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
vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: undefined, isPending: true, error: null }),
}));
