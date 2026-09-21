import { useEffect, useState } from "react";
import { api, rows, text, type Row } from "@/lib/api";
import { useResource } from "@/lib/hooks";
import { DataTable, Details, ErrorNotice, Resource } from "@/components/data";
import { Button } from "@/components/ui/button";
export default function Jobs() {
  const [tick, setTick] = useState(0);
  const state = useResource<Row>("jobs", tick);
  const [kind, setKind] = useState("backup");
  const [input, setInput] = useState("{}");
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<Row>();
  const pending = rows(state.data).some((r) =>
    ["queued", "running", "pending"].includes(String(r.state)),
  );
  useEffect(() => {
    if (!pending) return;
    const timer = setInterval(() => setTick((t) => t + 1), 2000);
    return () => clearInterval(timer);
  }, [pending]);
  async function start() {
    setBusy(true);
    setError(undefined);
    try {
      setResult(
        await api.send<Row>("jobs", "POST", {
          kind,
          input: JSON.parse(input),
        }),
      );
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
        <h2>Configuration backup and restore</h2>
        <p>
          Backups cover authoritative configuration and required secrets. Query
          history and derived lists are separate.
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
            <select value={kind} onChange={(e) => setKind(e.target.value)}>
              <option value="backup">Create backup</option>
              <option value="restore">Restore backup</option>
              <option value="refresh">Refresh lists</option>
            </select>
          </label>
          <label>
            Job input (JSON)
            <textarea
              required
              value={input}
              onChange={(e) => setInput(e.target.value)}
              rows={2}
            />
          </label>
          <Button disabled={busy}>
            {busy
              ? "Starting…"
              : kind === "restore"
                ? "Validate and restore"
                : "Start job"}
          </Button>
        </form>
        {error && <ErrorNotice error={error} />}{" "}
        {result && <Details value={result} />}
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
            items={rows(state.data)}
            columns={["id", "kind", "state", "created", "error", "result"].map(
              (key) => ({
                key,
                label: key.replaceAll("_", " "),
                render: (r) => text(r[key]),
              }),
            )}
            empty="No jobs have been recorded."
          />
        </Resource>
      </section>
    </>
  );
}
