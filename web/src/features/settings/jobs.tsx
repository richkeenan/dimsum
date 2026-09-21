import { useEffect, useState } from "react";
import {
  api,
  archiveBase64,
  backupURL,
  text,
  type Job,
  type Settings,
} from "@/lib/api";
import { useResource } from "@/lib/hooks";
import { DataTable, ErrorNotice, Resource } from "@/components/data";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export default function Jobs() {
  const [tick, setTick] = useState(0);
  const state = useResource<{ items: Job[] }>("jobs", tick);
  const [kind, setKind] = useState("backup");
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
      if (!settings.revision)
        throw new Error("The saved configuration revision is unavailable.");
      const data = await archiveBase64(file);
      setArchive({ name: file.name, data, revision: settings.revision });
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  async function start() {
    setError(undefined);
    setBusy(true);
    setStarted(undefined);
    try {
      if (kind === "restore" && !archive)
        throw new Error("Select a configuration archive first.");
      const input =
        kind === "restore"
          ? { revision: archive!.revision, archive: archive!.data }
          : kind === "upstream-probe"
            ? { endpoint: endpoint.trim() }
            : {};
      const job = await api.send<Job>("jobs", "POST", { kind, input });
      setStarted(job.id);
      setTick((t) => t + 1);
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <section className="panel inset">
        <h2>Backup, restore and diagnostics</h2>
        <p>
          Archives contain authoritative configuration and required secrets.
          Query history and downloaded lists are excluded. Downloads remain
          available until the next backup or service restart.
        </p>
        <form
          className="inline-form"
          onSubmit={(e) => {
            e.preventDefault();
            void start();
          }}
        >
          <label>
            Operation
            <select
              aria-label="Operation"
              value={kind}
              onChange={(e) => {
                setKind(e.target.value);
                setError(undefined);
              }}
              disabled={busy}
            >
              <option value="backup">Create backup</option>
              <option value="restore">Restore archive</option>
              <option value="refresh">Refresh lists</option>
              <option value="upstream-probe">Probe configured upstream</option>
              <option value="support-bundle">
                Create redacted support bundle
              </option>
            </select>
          </label>
          {kind === "upstream-probe" && (
            <label>
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
          {kind === "restore" && (
            <label>
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
          )}
          <Button disabled={busy || (kind === "restore" && !archive)}>
            {busy
              ? "Working…"
              : kind === "restore"
                ? "Validate and restore"
                : "Start job"}
          </Button>
        </form>
        {kind === "restore" && archive && (
          <div className="revision">
            <span>{archive.name}</span>
            <span>
              Restore against saved revision <code>{archive.revision}</code>
            </span>
          </div>
        )}
        {error && <ErrorNotice error={error} />}
        {started && (
          <p role="status">
            Job {started}: {latest?.state ?? "accepted"}
          </p>
        )}
        {latest?.state === "failed" && (
          <div className="notice danger" role="alert">
            {latest.error ?? "Job failed. Review the job result."}
            {latest.kind === "restore" && (
              <p>
                Reselect the archive to read the current saved revision before
                retrying.
              </p>
            )}
          </div>
        )}
      </section>
      <section className="panel">
        <div className="panel-heading">
          <h2>Background jobs</h2>
          <Button variant="outline" onClick={() => setTick((t) => t + 1)}>
            Refresh
          </Button>
        </div>
        <Resource state={state}>
          <DataTable
            items={jobs}
            columns={[
              { key: "id", label: "ID" },
              { key: "kind", label: "Operation" },
              { key: "state", label: "State" },
              { key: "created", label: "Created" },
              { key: "error", label: "Error" },
              {
                key: "result",
                label: "Result",
                render: (r) => {
                  const url =
                    r.state === "succeeded" ? backupURL(r.result) : undefined;
                  if (url && r.id !== downloadable)
                    return "Superseded by a newer backup";
                  return url ? (
                    <a
                      className="text-button"
                      href={url}
                      download="dimsum-config.tar"
                    >
                      Download backup
                    </a>
                  ) : (
                    text(r.result)
                  );
                },
              },
            ]}
            empty="No jobs have been recorded."
          />
        </Resource>
      </section>
    </>
  );
}
