import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, expect, it, vi } from "vitest";
import { api, APIError } from "@/lib/api";
import { AgentAccess } from "./agents";

const metadata = {
  id: "token-1",
  name: "Desktop assistant",
  created_at: "2026-09-21T10:00:00Z",
};
const secret = "dimsum-test-secret-only-once";

beforeEach(() => {
  vi.restoreAllMocks();
  vi.spyOn(api, "get").mockResolvedValue({ items: [] });
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: vi.fn().mockResolvedValue(undefined) },
  });
});

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const view = render(
    <QueryClientProvider client={client}>
      <AgentAccess />
    </QueryClientProvider>,
  );
  return { ...view, client };
}

function create() {
  fireEvent.change(screen.getByLabelText("Token name"), {
    target: { value: " Desktop assistant " },
  });
  fireEvent.click(screen.getByRole("button", { name: "Create token" }));
}

it("creates once, copies generic connection fields, and forgets the secret on Done without caching or storing it", async () => {
  const storage = vi.spyOn(Storage.prototype, "setItem");
  const send = vi.spyOn(api, "send").mockImplementation(async () => {
    vi.mocked(api.get).mockResolvedValue({ items: [metadata] });
    return { ...metadata, token: secret };
  });
  const { client } = mount();
  expect(screen.getByLabelText("MCP URL")).toHaveValue(
    window.location.origin + "/mcp",
  );
  expect(
    screen.getByRole("link", { name: "OpenAPI specification" }),
  ).toHaveAttribute("href", "/api/v1/openapi.json");
  create();
  expect(await screen.findByLabelText("New token")).toHaveValue(secret);
  expect(send).toHaveBeenCalledExactlyOnceWith("tokens", "POST", {
    name: metadata.name,
  });
  expect(screen.getByRole("button", { name: "Create token" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Copy token" }));
  await screen.findByText("Copied to clipboard.");
  expect(navigator.clipboard.writeText).toHaveBeenLastCalledWith(secret);
  fireEvent.click(
    screen.getByRole("button", { name: "Copy connection fields" }),
  );
  expect(navigator.clipboard.writeText).toHaveBeenLastCalledWith(
    `URL: ${window.location.origin}/mcp\nAuthorization: Bearer ${secret}`,
  );
  await screen.findByRole("button", { name: `Revoke ${metadata.name}` });
  expect(storage).not.toHaveBeenCalled();
  expect(
    JSON.stringify(client.getQueryData(["api", "tokens", "tokens"])),
  ).not.toContain(secret);
  fireEvent.click(screen.getByRole("button", { name: "Done" }));
  expect(screen.queryByLabelText("New token")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("Connection fields")).not.toBeInTheDocument();
  expect(screen.getByText(metadata.name)).toBeInTheDocument();
});

it("keeps selectable text when clipboard access fails and clears the secret on unmount", async () => {
  vi.spyOn(api, "send").mockResolvedValue({ ...metadata, token: secret });
  vi.mocked(navigator.clipboard.writeText).mockRejectedValue(
    new Error("Denied"),
  );
  const view = mount();
  create();
  await screen.findByLabelText("New token");
  fireEvent.click(screen.getByRole("button", { name: "Copy token" }));
  await screen.findByText(
    "Could not copy. Select the text below and copy it manually.",
  );
  const field = screen.getByLabelText("New token") as HTMLInputElement;
  fireEvent.focus(field);
  expect(field.selectionStart).toBe(0);
  expect(field.selectionEnd).toBe(secret.length);
  expect(screen.getByLabelText("Connection fields")).toHaveValue(
    `URL: ${window.location.origin}/mcp\nAuthorization: Bearer ${secret}`,
  );
  view.unmount();
  mount();
  expect(screen.queryByLabelText("New token")).not.toBeInTheDocument();
});

it("revokes a token, disables pending actions, and refreshes the token list", async () => {
  vi.mocked(api.get).mockResolvedValue({ items: [metadata] });
  let finish!: (value: { revoked: boolean }) => void;
  const send = vi.spyOn(api, "send").mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  mount();
  const revoke = await screen.findByRole("button", {
    name: `Revoke ${metadata.name}`,
  });
  expect(screen.getByText(/Created/).querySelector("time")).toHaveAttribute(
    "datetime",
    metadata.created_at,
  );
  fireEvent.click(revoke);
  expect(revoke).toBeDisabled();
  expect(revoke).toHaveTextContent("Revoking…");
  expect(send).toHaveBeenCalledExactlyOnceWith("tokens/token-1", "DELETE");
  vi.mocked(api.get).mockResolvedValue({ items: [] });
  finish({ revoked: true });
  await screen.findByText("No agent tokens yet.");
  expect(
    screen.queryByRole("button", { name: `Revoke ${metadata.name}` }),
  ).not.toBeInTheDocument();
});

it("preserves the name and offers retry after failed creation without showing a secret", async () => {
  const send = vi
    .spyOn(api, "send")
    .mockRejectedValue(new Error("Token could not be created."));
  mount();
  create();
  await screen.findByText("Token could not be created.");
  expect(screen.getByLabelText("Token name")).toHaveValue(
    " Desktop assistant ",
  );
  expect(screen.queryByLabelText("New token")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Create token" })).toBeEnabled();
  expect(send).toHaveBeenCalledTimes(1);
});

it("shows loading and a recoverable list error", async () => {
  vi.mocked(api.get).mockRejectedValue(
    new APIError(403, "forbidden", "Cannot list tokens."),
  );
  mount();
  expect(screen.getByText("Loading tokens…")).toBeInTheDocument();
  await screen.findByText("Cannot list tokens.");
  vi.mocked(api.get).mockResolvedValue({ items: [] });
  fireEvent.click(screen.getByRole("button", { name: /retry/i }));
  await screen.findByText("No agent tokens yet.");
});

it("retains a token after a failed revoke", async () => {
  vi.mocked(api.get).mockResolvedValue({ items: [metadata] });
  vi.spyOn(api, "send").mockRejectedValue(new Error("Revoke failed."));
  mount();
  fireEvent.click(
    await screen.findByRole("button", { name: `Revoke ${metadata.name}` }),
  );
  await screen.findByText("Revoke failed.");
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: `Revoke ${metadata.name}` }),
    ).toBeEnabled(),
  );
});
