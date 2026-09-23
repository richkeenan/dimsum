import { useEffect, useState } from "react";
import { ChevronRight } from "lucide-react";
import { api, archiveBase64, backupURL, text, type Job, type Settings } from "@/lib/api";
import { useResource } from "@/lib/hooks";
import { DataTable, Details, ErrorNotice, Resource } from "@/components/data";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export default function Jobs() {
  const [tick, setTick] = useState(0);
  const state = useResource<{ items: Job[] }>("jobs", tick);
  const [kind, setKind] = useState("upstream-probe");
  const [endpoint, setEndpoint] = useState("");
  const [archive, setArchive] = useState<{
    name: string;
    data: string;
    revision: string;
  }>();
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  const [started, setStarted] = useState<string>();
  const jobs = state.data?.items ?? [];
  const pending = jobs.some((j) => j.state === "running");
  const latest = jobs.find((j) => j.id === started);
  const downloadable = jobs
    .filter((j) => j.kind === "backup" && j.state === "succeeded")
    .at(-1)?.id;
  useEffect(() => {
    if (!pending) return;
    const timer = setInterval(() => setTick((t) => t + 1), 1000);
    return () => clearInterval(timer);
  }, [pending]);
  async function selectFile(file?: File) {
    setError(undefined);
    setArchive(undefined);
    if (!file) return;
    setBusy(true);
    try {
      const settings = await api.get<Settings>("settings");
      if (!settings.revision) throw new Error("The saved configuration revision is unavailable.");
      const data = await archiveBase64(file);
      setArchive({ name: file.name, data, revision: settings.revision });
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  async function start(operation = kind) {
    setError(undefined);
    setBusy(true);
    setStarted(undefined);
    try {
      if (operation === "restore" && !archive)
        throw new Error("Select a configuration archive first.");
      const input =
        operation === "restore"
          ? { revision: archive!.revision, archive: archive!.data }
          : operation === "upstream-probe"
            ? { endpoint: endpoint.trim() }
            : {};
      const job = await api.send<Job>("jobs", "POST", {
        kind: operation,
        input,
      });
      setStarted(job.id);
      setTick((t) => t + 1);
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="min-w-0 [&_p]:leading-relaxed">
      <section className="mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background p-5">
        <h2 className="mb-3 text-sm font-medium">Back up your configuration</h2>
        <p className="mb-[18px] text-xs text-muted-foreground">
          Includes configuration and secrets. Excludes query history, downloaded lists and DHCP
          leases.
        </p>
        <Button disabled={busy} onClick={() => start("backup")}>
          {busy ? "Working…" : "Create backup"}
        </Button>
        {downloadable && (
          <p className="mt-3 text-xs">
            <a
              className="border-0 bg-transparent p-0 text-left text-foreground hover:underline"
              href={backupURL(jobs.find((j) => j.id === downloadable)?.result)}
              download="dimsum-config.tar"
            >
              Download latest backup
            </a>
          </p>
        )}
      </section>
      <section className="mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background p-5">
        <h2 className="mb-3 text-sm font-medium">Restore a backup</h2>
        <p className="mb-[18px] text-xs text-muted-foreground">
          Replaces your saved configuration.
        </p>
        <form
          className="flex min-w-0 flex-wrap items-end gap-3 [&>*]:min-w-0"
          onSubmit={(e) => {
            e.preventDefault();
            void start("restore");
          }}
        >
          <label className="flex min-w-0 basis-40 flex-1 flex-col gap-1.5 text-xs font-normal">
            Configuration archive (.tar, up to 2 MiB)
            <Input
              type="file"
              accept=".tar,application/x-tar"
              disabled={busy}
              onChange={(e) => {
                const file = e.target.files?.[0];
                e.target.value = "";
                void selectFile(file);
              }}
            />
          </label>
          <Button disabled={busy || !archive}>{busy ? "Working…" : "Validate and restore"}</Button>
        </form>
      </section>
      <details className="mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background p-5 text-xs">
        <summary className="cursor-pointer text-muted-foreground">
          Diagnostics and maintenance
        </summary>
        <form
          className="mt-4 flex min-w-0 flex-wrap items-end gap-3 [&>*]:min-w-0"
          onSubmit={(e) => {
            e.preventDefault();
            void start();
          }}
        >
          <label className="flex min-w-0 basis-40 flex-1 flex-col gap-1.5 text-xs font-normal">
            Operation
            <select
              className="min-h-9 w-full min-w-0 rounded-md border border-input bg-background px-2.5 py-2 text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:opacity-50"
              aria-label="Operation"
              value={kind}
              onChange={(e) => {
                setKind(e.target.value);
                setError(undefined);
              }}
              disabled={busy}
            >
              <option value="refresh">Update blocklists</option>
              <option value="upstream-probe">Probe configured upstream</option>
              <option value="support-bundle">Create redacted support bundle</option>
            </select>
          </label>
          {kind === "upstream-probe" && (
            <label className="flex min-w-0 basis-40 flex-1 flex-col gap-1.5 text-xs font-normal">
              Configured upstream (IP:port)
              <Input
                value={endpoint}
                onChange={(e) => setEndpoint(e.target.value)}
                required
                placeholder="1.1.1.1:53"
                disabled={busy}
              />
            </label>
          )}
          <Button disabled={busy}>
            {busy
              ? "Working…"
              : kind === "upstream-probe"
                ? "Test upstream"
                : kind === "refresh"
                  ? "Update blocklists"
                  : "Create support bundle"}
          </Button>
        </form>
      </details>
      {error && <ErrorNotice error={error} />}
      {started && (
        <p className="my-3 text-xs text-muted-foreground" role="status">
          {latest?.state === "succeeded"
            ? "Operation completed."
            : latest?.state === "failed"
              ? "Operation failed."
              : "Operation in progress…"}
        </p>
      )}
      {latest?.state === "failed" && (
        <div
          className="my-3 rounded-[5px] border border-border border-l-[3px] border-l-[#b76763] bg-muted px-3.5 py-3 text-xs [overflow-wrap:anywhere] [&>p]:mt-1 [&>p]:mb-2"
          role="alert"
        >
          {latest.error ?? "Job failed. Review the job result."}
          {latest.kind === "restore" && (
            <p>Reselect the archive to read the current saved revision before retrying.</p>
          )}
        </div>
      )}
      <section className="mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background">
        <div className="flex flex-wrap items-center justify-between gap-2.5 border-b border-border px-[18px] py-[13px]">
          <h2 className="text-sm font-medium">Background jobs</h2>
          <Button variant="outline" onClick={() => setTick((t) => t + 1)}>
            Refresh
          </Button>
        </div>
        <Resource state={state}>
          <DataTable
            items={jobs}
            initialSorting={[{ id: "created", desc: true }]}
            sortScope="Sorting applies to the loaded job history."
            columns={[
              {
                key: "kind",
                label: "Operation",
                sortValue: (r) => jobLabel(r.kind),
                render: (r) => jobLabel(r.kind),
              },
              { key: "state", label: "State" },
              {
                key: "created",
                label: "Started",
                sortType: "datetime",
                render: (r) => (r.created ? new Date(String(r.created)).toLocaleString() : "—"),
              },
              {
                key: "result",
                label: "Result",
                sortable: false,
                render: (r) => {
                  const url = r.state === "succeeded" ? backupURL(r.result) : undefined;
                  if (url && r.id !== downloadable) return "Superseded by a newer backup";
                  return url ? (
                    <a
                      className="border-0 bg-transparent p-0 text-left text-foreground hover:underline"
                      href={url}
                      download="dimsum-config.tar"
                    >
                      Download backup
                    </a>
                  ) : (
                    <details className="group min-w-0">
                      <Button asChild size="sm" variant="outline">
                        <summary className="cursor-pointer list-none [&::-webkit-details-marker]:hidden">
                          <ChevronRight aria-hidden="true" className="group-open:rotate-90" />
                          {r.error ? "View error" : "Details"}
                        </summary>
                      </Button>
                      <Details value={{ id: r.id, error: r.error, result: r.result }} />
                    </details>
                  );
                },
              },
            ]}
            empty="No jobs have been recorded."
          />
        </Resource>
      </section>
    </div>
  );
}

function jobLabel(kind: unknown) {
  return (
    (
      {
        backup: "Backup",
        restore: "Restore",
        refresh: "Blocklist update",
        "upstream-probe": "Upstream test",
        "support-bundle": "Support bundle",
      } as Record<string, string>
    )[String(kind)] ?? text(kind)
  );
}
