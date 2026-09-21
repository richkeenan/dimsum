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
  onFilterChange,
  liveAllowed = true,
}: {
  range: string;
  initialFilter: Record<string, string>;
  refresh: number;
  onLiveTick: () => void;
  onFilterChange?: (filters: Record<string, string>) => void;
  liveAllowed?: boolean;
}) {
  const [filters, setFilters] = useState(initialFilter);
  const [draft, setDraft] = useState(initialFilter);
  const [cursors, setCursors] = useState<string[]>([""]);
  const [selected, setSelected] = useState<string>();
  const [live, setLive] = useState(true);
  const [technical, setTechnical] = useState(false);
  const [snapshot, setSnapshot] = useState<string>();
  const [tick, setTick] = useState(0);
  const invalidate = useCallback(() => {
    setTick((v) => v + 1);
    if (liveAllowed && cursors.length === 1 && !selected) onLiveTick();
  }, [onLiveTick, liveAllowed, cursors.length, selected]);
  const connection = useLive(
    liveAllowed && live && !selected && cursors.length === 1,
    invalidate,
  );
  const filterKey = JSON.stringify(Object.entries(initialFilter).sort());
  useEffect(() => {
    const next = Object.fromEntries(JSON.parse(filterKey)) as Record<
      string,
      string
    >;
    setFilters(next);
    setDraft(next);
    setCursors([""]);
    setSnapshot(undefined);
  }, [filterKey]);
  function applyFilters(next: Record<string, string>) {
    setFilters(next);
    setDraft(next);
    setCursors([""]);
    setSnapshot(undefined);
    onFilterChange?.(next);
  }
  function inspect(row: Row) {
    setSnapshot(snapshot ?? range);
    setSelected(text(row.id));
  }
  const query = queryParameters(filters, cursors.at(-1));
  function filterIdentity(
    row: Row,
    key: "rule_id" | "upstream_id" | "source_id",
  ) {
    const next = { ...filters, [key]: text(row[key]) };
    if (key !== "source_id") {
      next.boot_id = text(row.boot_id);
      next.generation = text(row.generation);
    }
    applyFilters(next);
    setSelected(undefined);
  }
  const state = useResource<Page>(
    "queries?" + (snapshot ?? range) + "&" + query,
    refresh + tick,
  );
  return (
    <>
      <form
        className="filters"
        onSubmit={(e) => {
          e.preventDefault();
          applyFilters(draft);
        }}
      >
        {["name", "client", "outcome"].map((key) => (
          <label key={key}>
            {key === "name"
              ? "Domain (exact)"
              : key === "client"
                ? "Client"
                : "Result"}
            {key === "outcome" ? (
              <select
                aria-label="Filter outcome"
                value={draft[key] ?? ""}
                onChange={(e) => setDraft({ ...draft, [key]: e.target.value })}
              >
                <option value="">All outcomes</option>
                {outcomes.map((o) => (
                  <option key={o} value={o}>
                    {resultLabel(o)}
                  </option>
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
        <details className="advanced-filters">
          <summary>Advanced filters</summary>
          <div className="form-grid">
            {Object.entries({
              qtype: "Type",
              source_id: "Source ID",
              rule_id: "Rule ID",
              upstream_id: "Upstream ID",
              boot_id: "Boot ID",
              generation: "Generation",
            }).map(([key, label]) => (
              <label key={key}>
                {label}
                <Input
                  aria-label={"Filter " + key}
                  value={draft[key] ?? ""}
                  onChange={(e) =>
                    setDraft({ ...draft, [key]: e.target.value })
                  }
                />
              </label>
            ))}
          </div>
          <p className="muted">
            Rule and upstream IDs need a boot ID and generation. Use query
            details to capture that scope. Source IDs match archived identities.
          </p>
        </details>
        <Button type="submit">Apply filters</Button>
        <Button
          type="button"
          variant="outline"
          onClick={() => {
            applyFilters({});
          }}
        >
          Clear
        </Button>
      </form>
      <div className="toolbar">
        <span role="status">
          {selected
            ? "Paused while inspecting"
            : cursors.length > 1
              ? "Paused on older queries"
              : !liveAllowed
                ? "Fixed time range"
                : !live
                  ? "Live updates paused"
                  : connection}
        </span>
        <Button
          variant="outline"
          disabled={!liveAllowed}
          onClick={() => setLive(!live)}
        >
          {live ? "Pause live" : "Start live"}
        </Button>
        <label>
          <input
            type="checkbox"
            checked={technical}
            onChange={(e) => setTechnical(e.target.checked)}
          />{" "}
          Technical columns
        </label>
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
                width: 100,
                render: (r) => (
                  <button
                    className="text-button"
                    onClick={() => inspect(r)}
                    title={text(r.time)}
                    style={{
                      whiteSpace: "nowrap",
                      fontVariantNumeric: "tabular-nums",
                    }}
                  >
                    {shortTime(r.time)}
                  </button>
                ),
              },
              {
                key: "client",
                label: "Client",
                width: 190,
                render: (r) => (
                  <span style={{ whiteSpace: "nowrap" }}>
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
                label: "Domain",
                width: 300,
                render: (r) => (
                  <button
                    className="text-button dns"
                    onClick={() => inspect(r)}
                    style={{ whiteSpace: "nowrap" }}
                  >
                    {text(r.name)}
                  </button>
                ),
              },
              { key: "qtype", label: "Type", width: 80 },
              {
                key: "outcome",
                label: "Result",
                width: 140,
                render: (r) => (
                  <span className={"outcome " + text(r.outcome)}>
                    {resultLabel(r.outcome)}
                  </span>
                ),
              },
              {
                key: "duration_us",
                label: "ms",
                width: 90,
                align: "right",
                render: (r) => microsecondsToMS(r.duration_us),
              },
              {
                key: "rule_id",
                label: "Rule ID",
                hidden: !technical,
                render: (r) => (
                  <button
                    className="text-button"
                    onClick={() => filterIdentity(r, "rule_id")}
                  >
                    {text(r.rule_id)}
                  </button>
                ),
              },
              {
                key: "source_id",
                label: "Source ID",
                hidden: !technical,
                render: (r) =>
                  r.source_id ? (
                    <button
                      className="text-button"
                      onClick={() => filterIdentity(r, "source_id")}
                    >
                      {text(r.source_id)}
                    </button>
                  ) : (
                    "Unavailable"
                  ),
              },
              {
                key: "upstream_id",
                label: "Upstream ID",
                hidden: !technical,
                render: (r) => (
                  <button
                    className="text-button"
                    onClick={() => filterIdentity(r, "upstream_id")}
                  >
                    {text(r.upstream_id)}
                  </button>
                ),
              },
              { key: "generation", label: "Generation", hidden: !technical },
            ]}
          />
        </section>
        <div className="toolbar">
          <span>Page {cursors.length} · up to 100 queries</span>
          <div className="actions">
            <Button
              variant="outline"
              disabled={cursors.length === 1 || state.isFetching}
              onClick={() => {
                if (cursors.length === 2) setSnapshot(undefined);
                setCursors((c) => c.slice(0, -1));
              }}
            >
              Previous
            </Button>
            <Button
              variant="outline"
              disabled={!state.data?.next_cursor || state.isFetching}
              onClick={() => {
                setSnapshot(snapshot ?? range);
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
          if (!open) {
            setSelected(undefined);
            if (cursors.length === 1) setSnapshot(undefined);
          }
        }}
      >
        <DialogContent side>
          <DialogTitle>Query detail</DialogTitle>
          <DialogDescription>
            Historical explanation from the query’s policy generation.
          </DialogDescription>
          {selected && (
            <QueryDetail
              key={selected}
              id={selected}
              filterIdentity={filterIdentity}
            />
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}
export function shortTime(value: unknown) {
  const date = new Date(text(value));
  return Number.isNaN(date.getTime())
    ? text(value)
    : date.toLocaleTimeString([], {
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hour12: false,
      });
}
export function resultLabel(value: unknown) {
  return (
    (
      {
        local: "Local answer",
        blocked: "Blocked",
        cache: "Cached",
        stale: "Cached (stale)",
        forwarded: "Forwarded",
        error: "Failed",
        rejected: "Rejected",
      } as Record<string, string>
    )[text(value)] ?? text(value)
  );
}
function QueryDetail({
  id,
  filterIdentity,
}: {
  id: string;
  filterIdentity: (
    row: Row,
    key: "rule_id" | "source_id" | "upstream_id",
  ) => void;
}) {
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
      <h3 className="dns">{name || "Root domain"}</h3>
      <span className={"outcome " + text(state.data?.outcome)}>
        {resultLabel(state.data?.outcome)}
      </span>
      <Details
        value={{
          Client: state.data?.client_name || state.data?.client,
          ...(state.data?.client_name ? { Address: state.data.client } : {}),
          Time: state.data?.time,
          Type: state.data?.qtype,
          "Duration (ms)": microsecondsToMS(state.data?.duration_us),
        }}
      />
      {!!state.data?.rule_description_available && (
        <p>{text(state.data.rule_description)}</p>
      )}
      {!!state.data?.alias_available && (
        <p>
          Matched alias: <span className="dns">{text(state.data.alias)}</span>
        </p>
      )}
      <div className="actions">
        {(["rule_id", "source_id", "upstream_id"] as const).map((key) =>
          state.data?.[key] && text(state.data[key]) !== "0" ? (
            <Button
              key={key}
              variant="outline"
              onClick={() => filterIdentity(state.data!, key)}
            >
              Queries for this{" "}
              {key === "rule_id"
                ? "rule"
                : key === "source_id"
                  ? "source"
                  : "upstream"}
            </Button>
          ) : null,
        )}
      </div>
      {!!state.data?.source_id && <p>Source: {text(state.data.source_id)}</p>}
      <details>
        <summary>Technical details</summary>
        <p className="muted">
          Historical policy scope. Unavailable fields were not retained.
        </p>
        <Details value={state.data} />
      </details>
      <section className="panel inset">
        <h3>Create a rule</h3>
        <div className="form-grid">
          <label>
            Action
            <select value={action} onChange={(e) => setAction(e.target.value)}>
              <option value="allow">Allow</option>
              <option value="deny">Block</option>
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
          {busy
            ? "Saving…"
            : `Create ${action === "deny" ? "block" : "allow"} rule`}
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
