import { useCallback, useEffect, useState } from "react";
import { ClientIdentity } from "@/components/client-identity";
import type { Device } from "@/lib/api";
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
import { DataTable, Details, ErrorNotice, Resource } from "@/components/data";
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
        className="mb-[18px] flex flex-wrap items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          applyFilters(draft);
        }}
      >
        {["name", "client", "outcome"].map((key) => (
          <label
            className="mb-4 flex min-w-[100px] max-w-[190px] flex-1 flex-col gap-1.5 text-xs font-normal"
            key={key}
          >
            {key === "name"
              ? "Domain (exact)"
              : key === "client"
                ? "Client"
                : "Result"}
            {key === "outcome" ? (
              <select
                className="min-h-9 min-w-0 rounded-md border border-input bg-background px-2.5 py-2 text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
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
        <details className="min-w-0 border-t border-border px-5 py-3 text-xs">
          <summary className="cursor-pointer text-muted-foreground">
            Advanced filters
          </summary>
          <div className="mt-3.5 mb-[22px] grid min-w-0 grid-cols-1 gap-4 min-[701px]:grid-cols-2">
            {Object.entries({
              qtype: "Type",
              source_id: "Source ID",
              rule_id: "Rule ID",
              upstream_id: "Upstream ID",
              boot_id: "Boot ID",
              generation: "Generation",
            }).map(([key, label]) => (
              <label
                className="flex min-w-0 flex-col gap-1.5 text-xs font-normal"
                key={key}
              >
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
          <p className="my-2.5 max-w-[75ch] text-xs leading-relaxed text-muted-foreground">
            Rule and upstream IDs need a boot ID and generation. Use query
            details to capture that scope. Source IDs match archived identities.
          </p>
        </details>
        <Button className="mb-4" type="submit">
          Apply filters
        </Button>
        <Button
          className="mb-4"
          type="button"
          variant="outline"
          onClick={() => {
            applyFilters({});
          }}
        >
          Clear
        </Button>
      </form>
      <div className="my-[15px] flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground">
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
        <label className="flex flex-row items-center gap-1.5 text-xs font-normal">
          <input
            className="size-4 accent-primary"
            type="checkbox"
            checked={technical}
            onChange={(e) => setTechnical(e.target.checked)}
          />{" "}
          Technical columns
        </label>
      </div>
      <Resource state={state} retry={invalidate}>
        <section className="mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background">
          <DataTable
            items={rows(state.data)}
            columns={[
              {
                key: "time",
                label: "Time",
                width: 100,
                render: (r) => (
                  <button
                    className="border-0 bg-transparent p-0 text-left whitespace-nowrap text-foreground tabular-nums hover:underline"
                    onClick={() => inspect(r)}
                    title={text(r.time)}
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
                  <ClientIdentity
                    source={
                      r.client_name_source
                        ? String(r.client_name_source)
                        : undefined
                    }
                    address={text(r.client)}
                    name={r.client_name ? String(r.client_name) : undefined}
                    device={r.client_device as Device | undefined}
                    stale={r.client_name_fresh === false}
                  />
                ),
              },
              {
                key: "name",
                label: "Domain",
                width: 300,
                render: (r) => (
                  <button
                    className="border-0 bg-transparent p-0 text-left text-base whitespace-nowrap text-foreground hover:underline"
                    onClick={() => inspect(r)}
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
                  <span
                    className={`inline-block rounded px-[7px] py-[3px] text-xs ${r.outcome === "blocked" ? "bg-amber-100 text-amber-900 dark:bg-amber-950 dark:text-amber-200" : r.outcome === "error" || r.outcome === "rejected" ? "bg-destructive/10 text-destructive" : r.outcome === "stale" ? "bg-muted text-muted-foreground" : "bg-accent text-foreground"}`}
                  >
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
                    className="border-0 bg-transparent p-0 text-left text-foreground hover:underline"
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
                      className="border-0 bg-transparent p-0 text-left text-foreground hover:underline"
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
                    className="border-0 bg-transparent p-0 text-left text-foreground hover:underline"
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
        <div className="my-[15px] flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground">
          <span>Page {cursors.length} · up to 100 queries</span>
          <div className="flex flex-wrap items-center gap-2">
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
        <DialogContent
          side
          className="min-w-0 overflow-x-hidden [&>*]:min-w-0 [&_input]:min-w-0 [&_input]:w-full [&_select]:min-w-0 [&_select]:w-full"
        >
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
      <h3 className="text-sm font-medium wrap-anywhere">
        {name || "Root domain"}
      </h3>
      <span
        className={`inline-block w-fit rounded px-[7px] py-[3px] text-xs ${state.data?.outcome === "blocked" ? "bg-amber-100 text-amber-900 dark:bg-amber-950 dark:text-amber-200" : state.data?.outcome === "error" || state.data?.outcome === "rejected" ? "bg-destructive/10 text-destructive" : state.data?.outcome === "stale" ? "bg-muted text-muted-foreground" : "bg-accent text-foreground"}`}
      >
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
        <p className="text-sm leading-relaxed wrap-anywhere">
          {text(state.data.rule_description)}
        </p>
      )}
      {!!state.data?.alias_available && (
        <p className="text-sm leading-relaxed wrap-anywhere">
          Matched alias:{" "}
          <span className="text-xs wrap-anywhere">
            {text(state.data.alias)}
          </span>
        </p>
      )}
      <div className="flex flex-wrap items-center gap-2">
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
      {!!state.data?.source_id && (
        <p className="text-sm leading-relaxed wrap-anywhere">
          Source: {text(state.data.source_id)}
        </p>
      )}
      <details className="min-w-0 border-t border-border px-5 py-3 text-xs">
        <summary className="cursor-pointer text-muted-foreground">
          Technical details
        </summary>
        <p className="my-2.5 max-w-[75ch] text-xs leading-relaxed text-muted-foreground">
          Historical policy scope. Unavailable fields were not retained.
        </p>
        <Details value={state.data} />
      </details>
      <section className="mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background p-5">
        <h3 className="mb-3 text-sm font-medium">Create a rule</h3>
        <div className="mt-3.5 mb-[22px] grid min-w-0 grid-cols-1 gap-4 min-[701px]:grid-cols-2">
          <label className="flex min-w-0 flex-col gap-1.5 text-xs font-normal">
            Action
            <select
              className="min-h-9 min-w-0 rounded-md border border-input bg-background px-2.5 py-2 text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
              value={action}
              onChange={(e) => setAction(e.target.value)}
            >
              <option value="allow">Allow</option>
              <option value="deny">Block</option>
            </select>
          </label>
          <label className="flex min-w-0 flex-col gap-1.5 text-xs font-normal">
            Match scope
            <select
              className="min-h-9 min-w-0 rounded-md border border-input bg-background px-2.5 py-2 text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
              value={scope}
              onChange={(e) => setScope(e.target.value)}
            >
              <option value="exact">Exact name only</option>
              <option value="suffix">Name and descendants</option>
            </select>
          </label>
        </div>
        <div className="my-3 rounded-[5px] border border-l-[3px] border-border border-l-primary bg-muted px-3.5 py-3 text-xs wrap-anywhere">
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
            <p
              className="mt-3 text-xs leading-relaxed text-muted-foreground"
              role="status"
            >
              Rule saved. Check activation below.
            </p>
            <Details value={result} />
          </>
        )}
      </section>
    </Resource>
  );
}
