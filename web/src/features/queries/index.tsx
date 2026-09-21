import { useCallback, useEffect, useState } from "react";
import {
  api,
  rows,
  text,
  queryParameters,
  microsecondsToMS,
  outcomes,
  type Page,
  type Row,
  type Settings,
} from "@/lib/api";
import { useLive, useResource } from "@/lib/hooks";
import {
  Completeness,
  DataTable,
  Details,
  ErrorNotice,
  Resource,
} from "@/components/data";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
export default function Queries({
  range,
  initialFilter,
  refresh,
  onLiveTick,
}: {
  range: string;
  initialFilter: Record<string, string>;
  refresh: number;
  onLiveTick: () => void;
}) {
  const [filters, setFilters] = useState(initialFilter);
  const [draft, setDraft] = useState(initialFilter);
  const [cursors, setCursors] = useState<string[]>([""]);
  const [selected, setSelected] = useState<string>();
  const [live, setLive] = useState(false);
  const [tick, setTick] = useState(0);
  const invalidate = useCallback(() => {
    setTick((v) => v + 1);
    onLiveTick();
  }, [onLiveTick]);
  const connection = useLive(
    live && !selected && cursors.length === 1,
    invalidate,
  );
  useEffect(() => {
    setCursors([""]);
  }, [range]);
  const query = queryParameters(filters, cursors.at(-1));
  const state = useResource<Page>(
    "queries?" + range + "&" + query,
    refresh + tick,
  );
  return (
    <>
      <form
        className="filters"
        onSubmit={(e) => {
          e.preventDefault();
          setFilters(draft);
          setCursors([""]);
        }}
      >
        {["name", "client", "outcome", "qtype"].map((key) => (
          <label key={key}>
            {key === "qtype" ? "Type" : key === "name" ? "Exact name" : key}
            {key === "outcome" ? (
              <select
                aria-label="Filter outcome"
                value={draft[key] ?? ""}
                onChange={(e) => setDraft({ ...draft, [key]: e.target.value })}
              >
                <option value="">All outcomes</option>
                {outcomes.map((o) => (
                  <option key={o}>{o}</option>
                ))}
              </select>
            ) : (
              <Input
                aria-label={"Filter " + key}
                value={draft[key] ?? ""}
                onChange={(e) => setDraft({ ...draft, [key]: e.target.value })}
              />
            )}
          </label>
        ))}
        <Button type="submit">Filter</Button>
        <Button
          type="button"
          variant="outline"
          onClick={() => {
            setDraft({});
            setFilters({});
            setCursors([""]);
          }}
        >
          Clear
        </Button>
      </form>
      <div className="toolbar">
        <span role="status">
          {selected ? "Paused while inspecting" : connection}
        </span>
        <Button variant="outline" onClick={() => setLive(!live)}>
          {live ? "Pause live" : "Start live"}
        </Button>
      </div>
      <Resource state={state} retry={invalidate}>
        <Completeness meta={state.data} />
        <section className="panel">
          <DataTable
            items={rows(state.data)}
            columns={[
              {
                key: "time",
                label: "Time",
                render: (r) => (
                  <button
                    className="text-button"
                    onClick={() => setSelected(text(r.id))}
                  >
                    {text(r.time)}
                  </button>
                ),
              },
              {
                key: "client",
                label: "Client",
                render: (r) => (
                  <span>
                    {text(r.client_name || r.client)}
                    {!!r.client_name && (
                      <small>
                        {text(r.client)}
                        {r.client_name_fresh === false ? " · stale name" : ""}
                      </small>
                    )}
                  </span>
                ),
              },
              {
                key: "name",
                label: "Name",
                render: (r) => (
                  <button
                    className="text-button dns"
                    onClick={() => setSelected(text(r.id))}
                  >
                    {text(r.name)}
                  </button>
                ),
              },
              { key: "qtype", label: "Type" },
              {
                key: "outcome",
                label: "Result",
                render: (r) => (
                  <span className={"outcome " + text(r.outcome)}>
                    {text(r.outcome)}
                  </span>
                ),
              },
              { key: "rule_id", label: "Rule ID" },
              {
                key: "duration_us",
                label: "Time (ms)",
                render: (r) => microsecondsToMS(r.duration_us),
              },
              { key: "upstream_id", label: "Upstream ID" },
              { key: "generation", label: "Generation" },
            ]}
          />
        </section>
        <div className="toolbar">
          <span>Page {cursors.length} · up to 100 queries</span>
          <div className="actions">
            <Button
              variant="outline"
              disabled={cursors.length === 1}
              onClick={() => setCursors((c) => c.slice(0, -1))}
            >
              Previous
            </Button>
            <Button
              variant="outline"
              disabled={!state.data?.next_cursor}
              onClick={() => {
                setLive(false);
                setCursors((c) => [...c, state.data!.next_cursor!]);
              }}
            >
              Next
            </Button>
          </div>
        </div>
      </Resource>
      <Dialog
        open={!!selected}
        onOpenChange={(open) => {
          if (!open) setSelected(undefined);
        }}
      >
        <DialogContent side>
          <DialogTitle>Query detail</DialogTitle>
          <DialogDescription>
            Historical explanation from the query’s policy generation.
          </DialogDescription>
          {selected && <QueryDetail id={selected} />}
        </DialogContent>
      </Dialog>
    </>
  );
}
function QueryDetail({ id }: { id: string }) {
  const state = useResource<Row>("queries/" + encodeURIComponent(id));
  const [scope, setScope] = useState("exact");
  const [action, setAction] = useState("allow");
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<Row>();
  const name = text(state.data?.name);
  async function save() {
    setBusy(true);
    setError(undefined);
    try {
      const settings = await api.get<Settings>("settings");
      setResult(
        await api.send<Row>("rules", "POST", {
          revision: settings.revision,
          item: {
            id: "query-" + crypto.randomUUID(),
            kind: scope,
            action,
            pattern: name,
            enabled: true,
          },
        }),
      );
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Resource state={state}>
      <Details value={state.data} />
      <p className="muted">
        Absent fields were not retained or are unsupported. They cannot be
        reconstructed from current policy.
      </p>
      <section className="panel inset">
        <h3>Create a rule</h3>
        <div className="form-grid">
          <label>
            Action
            <select value={action} onChange={(e) => setAction(e.target.value)}>
              <option value="allow">Allow</option>
              <option value="deny">Deny</option>
            </select>
          </label>
          <label>
            Match scope
            <select value={scope} onChange={(e) => setScope(e.target.value)}>
              <option value="exact">Exact name only</option>
              <option value="suffix">Name and descendants</option>
            </select>
          </label>
        </div>
        <div className="notice">
          {scope === "exact"
            ? `Matches only ${name}. Subdomains are not included.`
            : `Matches ${name} and every descendant, including child.${name}.`}
        </div>
        {error && <ErrorNotice error={error} />}
        <Button disabled={busy || !state.data?.name} onClick={save}>
          {busy ? "Saving…" : `Create ${action} rule`}
        </Button>
        {result && (
          <>
            <p role="status">Rule saved. Check activation below.</p>
            <Details value={result} />
          </>
        )}
      </section>
    </Resource>
  );
}
