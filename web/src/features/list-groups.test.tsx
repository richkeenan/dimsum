import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, expect, it } from "vitest";
import { GroupedListTable } from "./list-groups";

const items = [
  {
    id: "renamed",
    category: "ads-trackers",
    label: "Example ads",
    enabled: true,
    default_apply: false,
  },
  {
    id: "security",
    category: "security",
    label: "Example threats",
    enabled: true,
    source: { error: "Download failed" },
  },
  { id: "custom", label: "My list", enabled: true },
];
const columns = [{ key: "label", label: "List" }];

beforeEach(() => localStorage.clear());

it("groups by API category and exposes defaults and errors while collapsed", () => {
  render(<GroupedListTable items={items} columns={columns} />);
  const ads = screen.getByRole("button", { name: /Ads & Trackers/ });
  expect(ads).toHaveAttribute("aria-expanded", "false");
  expect(ads).toHaveTextContent("1 list · 0 used by default");
  const security = screen.getByRole("button", { name: /Security & Scams/ });
  expect(security).toHaveTextContent("1 list · 1 used by default");
  expect(security).toHaveTextContent("1 issue");
  expect(screen.queryByText("Example ads")).not.toBeInTheDocument();
  fireEvent.click(ads);
  expect(
    within(screen.getByRole("region", { name: /Ads & Trackers/ })).getByText("Example ads"),
  ).toBeVisible();
  expect(screen.queryByText("Example threats")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: /Custom Lists/ }));
  expect(screen.getByText("My list")).toBeVisible();
});

it("remembers expanded groups and supports expanding and collapsing all", () => {
  const view = render(<GroupedListTable items={items} columns={columns} />);
  fireEvent.click(screen.getByRole("button", { name: /Security & Scams/ }));
  view.unmount();
  render(<GroupedListTable items={items} columns={columns} />);
  expect(screen.getByText("Example threats")).toBeVisible();
  expect(screen.queryByText("Example ads")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Expand all" }));
  expect(screen.getByText("Example ads")).toBeVisible();
  expect(screen.getByText("My list")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Collapse all" }));
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
});

it("keeps unknown categories accessible when saved browser preferences are invalid", () => {
  localStorage.setItem("dimsum-list-groups", "invalid json");
  render(
    <GroupedListTable
      items={[{ id: "future", category: "future-category", label: "Future list" }]}
      columns={columns}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: /Custom Lists/ }));
  expect(screen.getByText("Future list")).toBeVisible();
});
