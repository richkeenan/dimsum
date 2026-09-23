import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { api, type Job } from "@/lib/api";
import { BackupDownload } from "./backup";

afterEach(() => vi.restoreAllMocks());
const url = "/api/v1/config/backups/0123456789abcdef0123456789abcdef";
const running: Job = { id: "2", kind: "backup", state: "running", created: "2026-09-23T12:00:00Z" };

it("downloads only the requested backup once, even when completed jobs are polled again", async () => {
  const downloads: string[] = [];
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
    this: HTMLAnchorElement,
  ) {
    downloads.push(this.getAttribute("href")!);
  });
  vi.spyOn(api, "send").mockResolvedValue(running);
  const refresh = () => {};
  const older: Job = { ...running, id: "1", state: "succeeded", result: { download_url: url } };
  const view = render(<BackupDownload jobs={[older]} refresh={refresh} />);
  expect(downloads).toEqual([]);
  fireEvent.click(screen.getByRole("button", { name: "Download backup" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Preparing backup…" })).toBeDisabled(),
  );
  const completed: Job = { ...running, state: "succeeded", result: { download_url: url } };
  view.rerender(<BackupDownload jobs={[older, completed]} refresh={refresh} />);
  await waitFor(() => expect(downloads).toEqual([url]));
  view.rerender(<BackupDownload jobs={[older, { ...completed }]} refresh={refresh} />);
  expect(downloads).toEqual([url]);
  expect(screen.getByRole("link", { name: "Download again" })).toHaveAttribute(
    "download",
    "dimsum-config.tar",
  );
  expect(screen.getByRole("button", { name: "Download backup" })).toBeEnabled();
});

it("allows retry after a failed job without downloading an earlier archive", async () => {
  const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
  vi.spyOn(api, "send").mockResolvedValue(running);
  const refresh = () => {};
  const view = render(<BackupDownload jobs={[]} refresh={refresh} />);
  fireEvent.click(screen.getByRole("button", { name: "Download backup" }));
  await screen.findByRole("button", { name: "Preparing backup…" });
  view.rerender(
    <BackupDownload
      jobs={[{ ...running, state: "failed", error: "archive write failed" }]}
      refresh={refresh}
    />,
  );
  expect(await screen.findByText(/Couldn’t prepare the backup/)).toBeVisible();
  expect(screen.getByRole("button", { name: "Download backup" })).toBeEnabled();
  expect(screen.queryByText("archive write failed")).not.toBeInTheDocument();
  expect(click).not.toHaveBeenCalled();
});
