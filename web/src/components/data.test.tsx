import { useState } from "react";
import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it } from "vitest";
import { DataTable, type Column } from "./data";

function names() {
  return screen
    .getAllByRole("row")
    .slice(1)
    .map((row) => within(row).getAllByRole("cell")[0].textContent);
}

it("sorts exact counters in both directions with missing values last", () => {
  render(
    <DataTable
      items={[
        { name: "Missing", count: null },
        { name: "Larger", count: "9007199254740993" },
        { name: "Smaller", count: "9007199254740992" },
        { name: "Ten", count: "10" },
        { name: "Two", count: "2" },
      ]}
      columns={[
        { key: "name", label: "Name" },
        { key: "count", label: "Queries", sortType: "number" },
      ]}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Queries" }));
  expect(names()).toEqual(["Larger", "Smaller", "Ten", "Two", "Missing"]);
  expect(screen.getByRole("columnheader", { name: "Queries" })).toHaveAttribute(
    "aria-sort",
    "descending",
  );
  fireEvent.click(screen.getByRole("button", { name: "Queries" }));
  expect(names()).toEqual(["Two", "Ten", "Smaller", "Larger", "Missing"]);
});

it.each([
  {
    sortType: "datetime" as const,
    values: ["2026-09-23T09:00:00-02:00", "2026-09-23T10:00:00Z", null],
    expected: ["First", "Second", "Missing"],
  },
  {
    sortType: "address" as const,
    values: ["192.0.2.10", "192.0.2.2", null],
    expected: ["Second", "First", "Missing"],
  },
  {
    sortType: "address" as const,
    values: ["2001:db8::10", "2001:db8:0:0:0:0:0:2", null],
    expected: ["Second", "First", "Missing"],
  },
])("sorts $sortType values rather than formatted text", ({ sortType, values, expected }) => {
  render(
    <DataTable
      items={values.map((value, i) => ({ name: ["First", "Second", "Missing"][i], value }))}
      columns={[
        { key: "name", label: "Name" },
        { key: "value", label: "Value", sortType },
      ]}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Value" }));
  expect(names()).toEqual(expected);
});

function Editor({ name }: { name: string }) {
  const [value, setValue] = useState("");
  return (
    <input
      aria-label={`Note for ${name}`}
      value={value}
      onChange={(e) => setValue(e.target.value)}
    />
  );
}

it("stops applying a sort when its technical column is hidden", () => {
  const items = [
    { id: "a", name: "Alpha", generation: "10" },
    { id: "b", name: "Beta", generation: "2" },
  ];
  const columns: Column[] = [
    { key: "name", label: "Name" },
    { key: "generation", label: "Version", sortType: "number" },
  ];
  const view = render(<DataTable items={items} columns={columns} />);
  fireEvent.click(screen.getByRole("button", { name: "Version" }));
  fireEvent.click(screen.getByRole("button", { name: "Version" }));
  expect(names()).toEqual(["Beta", "Alpha"]);
  view.rerender(
    <DataTable items={items} columns={[columns[0], { ...columns[1], hidden: true }]} />,
  );
  expect(names()).toEqual(["Alpha", "Beta"]);
});

it("orders mixed displayed names and IPv6 addresses without treating hex digits as text", () => {
  render(
    <DataTable
      items={[
        { name: "Zulu" },
        { name: "2001:db8::10" },
        { name: "2001:db8:0:0:0:0:0:f" },
        { name: "alpha" },
      ]}
      columns={[{ key: "name", label: "Device", sortType: "address" }]}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Device" }));
  expect(names()).toEqual(["2001:db8:0:0:0:0:0:f", "2001:db8::10", "alpha", "Zulu"]);
});

it("reverses timestamps within one millisecond without losing fractional precision", () => {
  render(
    <DataTable
      items={[
        { name: "Later", time: "2026-09-23T10:00:00.000999Z" },
        { name: "Earlier", time: "2026-09-23T12:00:00.000001+02:00" },
      ]}
      columns={[
        { key: "name", label: "Name" },
        { key: "time", label: "Time", sortType: "datetime" },
      ]}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Time" }));
  expect(names()).toEqual(["Later", "Earlier"]);
  fireEvent.click(screen.getByRole("button", { name: "Time" }));
  expect(names()).toEqual(["Earlier", "Later"]);
});

it("sorts displayed labels by keyboard and preserves cell state and sorting on refresh", async () => {
  const user = userEvent.setup();
  const columns: Column[] = [
    { key: "name", label: "Device", sortValue: (r) => r.label, render: (r) => String(r.label) },
    {
      key: "actions",
      label: "Actions",
      sortable: false,
      render: (r) => <Editor name={String(r.label)} />,
    },
  ];
  const items = [
    { id: "1", label: "Zulu" },
    { id: "2", label: "Alpha" },
  ];
  const view = render(<DataTable items={items} columns={columns} />);
  await user.type(screen.getByRole("textbox", { name: "Note for Zulu" }), "keep me");
  screen.getByRole("button", { name: "Device" }).focus();
  await user.keyboard("{Enter}");
  expect(names()).toEqual(["Alpha", "Zulu"]);
  expect(screen.queryByRole("button", { name: "Actions" })).not.toBeInTheDocument();
  view.rerender(
    <DataTable
      items={[...items.map((r) => ({ ...r })), { id: "3", label: "Beta" }]}
      columns={[...columns]}
    />,
  );
  expect(names()).toEqual(["Alpha", "Beta", "Zulu"]);
  expect(screen.getByRole("textbox", { name: "Note for Zulu" })).toHaveValue("keep me");
});
