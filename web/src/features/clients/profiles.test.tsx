import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { Profiles } from "./index";

vi.mock("@tanstack/react-router", () => ({ useBlocker: () => {} }));
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
const status = {
  saved_revision: "r1",
  active_revision: "r1",
  sources: [],
  pending: false,
  recovered: false,
  restart_required: false,
  active_generation: "1",
};
function setup() {
  const writes: any[] = [];
  let name = "Family";
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    if (init?.method === "PATCH") {
      const body = JSON.parse(String(init.body));
      writes.push(body);
      name = body.name ?? name;
      return Response.json(status);
    }
    if (url.includes("client-policy?")) {
      const params = new URL(url, "http://localhost").searchParams;
      return Response.json({
        status,
        scope: params.get("scope"),
        id: params.get("id") ?? "",
        desired: { id: params.get("id"), name, policy: {} },
        effective: {
          blocking: { value: true, source: {} },
          lists: { shared: { value: true, source: {} } },
          upstream: { upstreams: [] },
          upstream_source: {},
          rules: [],
        },
        active: null,
      });
    }
    if (url.endsWith("profiles")) return Response.json({ status, items: [{ id: "family", name }] });
    if (url.endsWith("lists"))
      return Response.json({
        items: [{ id: "shared", url: "builtin://work-compatibility", enabled: true }],
      });
    if (url.endsWith("catalog")) return Response.json({ items: [] });
    return Response.json({ status, items: [], observed_available: true, observed: { items: [] } });
  });
  render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <Profiles />
    </QueryClientProvider>,
  );
  return writes;
}
it("edits from the map in a dialog, protects drafts and restores the edit trigger", async () => {
  setup();
  fireEvent.click(screen.getByRole("button", { name: "Profiles" }));
  const trigger = await screen.findByRole("button", { name: "Edit Family profile" });
  // Mouse clicks need not focus buttons (notably Safari on macOS).
  screen.getByRole("button", { name: "Create profile" }).focus();
  fireEvent.click(trigger);
  const dialog = await screen.findByRole("dialog", { name: "Edit Family profile" });
  fireEvent.change(await within(dialog).findByLabelText("Name"), { target: { value: "Draft" } });
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
  expect(within(dialog).getByLabelText("Name")).toHaveValue("Draft");
  confirm.mockReturnValue(true);
  fireEvent.keyDown(dialog, { key: "Escape" });
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  await waitFor(() => expect(trigger).toHaveFocus());
  fireEvent.click(trigger);
  expect(await screen.findByLabelText("Name")).toHaveValue("Family");
});
it("saves in the dialog and links the shared list by subscription ID", async () => {
  const writes = setup();
  fireEvent.click(screen.getByRole("button", { name: "Profiles" }));
  fireEvent.click(await screen.findByRole("button", { name: "Edit Family profile" }));
  const dialog = await screen.findByRole("dialog", { name: "Edit Family profile" });
  expect(await within(dialog).findByRole("link", { name: "Edit shared list" })).toHaveAttribute(
    "href",
    "/lists?edit=shared",
  );
  fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "Study" } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Save 1 change" }));
  await waitFor(() =>
    expect(writes).toEqual([{ revision: "r1", scope: "profile", id: "family", name: "Study" }]),
  );
  expect(await screen.findByLabelText("Name")).toHaveValue("Study");
});

it("opens the editor immediately after creation and returns focus to Create when dismissed", async () => {
  const writes = setup();
  const trigger = screen.getByRole("button", { name: "Create profile" });
  trigger.focus();
  fireEvent.click(trigger);
  const create = screen.getByRole("dialog", { name: "Create profile" });
  fireEvent.change(within(create).getByLabelText("Name"), { target: { value: "New profile" } });
  const submit = within(create).getByRole("button", { name: "Create profile" });
  await waitFor(() => expect(submit).toBeEnabled());
  fireEvent.click(submit);
  await waitFor(() => expect(screen.getByRole("dialog")).toHaveAccessibleName(/^Edit .* profile$/));
  expect(await screen.findByLabelText("Name")).toHaveValue("New profile");
  expect(writes[0]).toMatchObject({ create: true, scope: "profile", name: "New profile" });
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  await waitFor(() => expect(trigger).toHaveFocus());
});

it("keeps creation open while its write is in flight", async () => {
  setup();
  const previous = globalThis.fetch;
  let finish!: (response: Response) => void;
  const pending = new Promise<Response>((resolve) => {
    finish = resolve;
  });
  vi.stubGlobal("fetch", (url: string, init?: RequestInit) =>
    init?.method === "PATCH" ? pending : previous(url, init),
  );
  vi.spyOn(window, "confirm").mockReturnValue(true);
  fireEvent.click(screen.getByRole("button", { name: "Create profile" }));
  const dialog = screen.getByRole("dialog");
  fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "New profile" } });
  const submit = within(dialog).getByRole("button", { name: "Create profile" });
  await waitFor(() => expect(submit).toBeEnabled());
  fireEvent.click(submit);
  expect(await screen.findByRole("button", { name: "Creating…" })).toBeDisabled();
  expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeDisabled();
  fireEvent.keyDown(dialog, { key: "Escape" });
  expect(dialog).toBeInTheDocument();
  finish(Response.json(status));
  await screen.findByRole("dialog", { name: /^Edit / });
});
