import { useCallback, useEffect, useRef, useState } from "react";
import { ClientFilter } from "./client-filter";
import { QueryDetail } from "./query-detail";
import { InlineRuleAction } from "./rule-action";
import {
  AnswerPreview,
  ResponseTime,
  ResultBadge,
  resultLabel,
} from "./response";
import { ClientIdentity } from "@/components/client-identity";
import type { Device } from "@/lib/api";
import {
  rows,
  count,
  text,
  queryParameters,
  outcomes,
  type Page,
  type Row,
} from "@/lib/api";
import { useLive, useResource } from "@/lib/hooks";
import { DataTable, Resource } from "@/components/data";
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
  const pendingFilter = useRef<ReturnType<typeof setTimeout> | undefined>(
    undefined,
  );
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
    clearTimeout(pendingFilter.current);
    const next = Object.fromEntries(JSON.parse(filterKey)) as Record<
      string,
      string
    >;
    setFilters(next);
    setDraft(next);
    setCursors([""]);
    setSnapshot(undefined);
    return () => clearTimeout(pendingFilter.current);
  }, [filterKey]);
  function applyFilters(next: Record<string, string>) {
    clearTimeout(pendingFilter.current);
    setFilters(next);
    setDraft(next);
    setCursors([""]);
    setSnapshot(undefined);
    onFilterChange?.(next);
  }
  function missingScope(next: Record<string, string>) {
    return (
      !!(next.rule_id?.trim() || next.upstream_id?.trim()) &&
      !(next.boot_id?.trim() && next.generation?.trim())
    );
  }
  function changeFilter(key: string, value: string, immediate = false) {
    const next = { ...draft, [key]: value };
    setDraft(next);
    clearTimeout(pendingFilter.current);
    if (missingScope(next)) return;
    if (immediate) applyFilters(next);
    else pendingFilter.current = setTimeout(() => applyFilters(next), 300);
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
        className="mb-4 space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          if (!missingScope(draft)) applyFilters(draft);
        }}
      >
        <div className="grid min-w-0 grid-cols-1 items-end gap-3 min-[701px]:grid-cols-[minmax(0,1.2fr)_minmax(0,1.2fr)_minmax(0,1fr)_auto]">
          {["name", "client", "outcome"].map((key) =>
            key === "client" ? (
              <div key={key} className="flex min-w-0 flex-col gap-1.5 text-xs">
                <span>Client</span>
                <ClientFilter
                  value={draft.client ?? ""}
                  range={range}
                  refresh={refresh}
                  onChange={(value) => changeFilter("client", value, true)}
                />
              </div>
            ) : (
              <label
                className="flex min-w-0 flex-col gap-1.5 text-xs font-normal"
                key={key}
              >
                {key === "name" ? "Domain (exact)" : "Result"}
                {key === "outcome" ? (
                  <select
                    className="h-9 min-w-0 rounded-md border border-input bg-background px-3 text-sm text-foreground shadow-xs focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                    aria-label="Filter outcome"
                    value={draft[key] ?? ""}
                    onChange={(e) => changeFilter(key, e.target.value, true)}
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
                    onChange={(e) => changeFilter(key, e.target.value)}
                  />
                )}
              </label>
            ),
          )}
          <Button
            className="justify-self-start"
            type="button"
            variant="outline"
            onClick={() => applyFilters({})}
          >
            Clear
          </Button>
        </div>
        <details className="min-w-0 border-b border-border pb-3 text-xs">
          <summary className="w-fit cursor-pointer rounded-sm py-1 text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">
            Advanced filters
          </summary>
          <div className="mt-3 grid min-w-0 grid-cols-1 gap-3 min-[501px]:grid-cols-2 min-[1001px]:grid-cols-3">
            {Object.entries({
              qtype: "Type",
              source_id: "Source ID",
              rule_id: "Rule ID",
              upstream_id: "Upstream ID",
              boot_id: "Boot ID",
              generation: "Configuration version",
            }).map(([key, label]) => (
              <label
                className="flex min-w-0 flex-col gap-1.5 text-xs font-normal"
                key={key}
              >
                {label}
                <Input
                  aria-label={"Filter " + key}
                  value={draft[key] ?? ""}
                  onChange={(e) => changeFilter(key, e.target.value)}
                />
              </label>
            ))}
          </div>
          <p className="my-2.5 max-w-[75ch] text-xs leading-relaxed text-muted-foreground">
            Rule and upstream IDs need a server run and configuration version.
            Use query details to capture that scope. Source IDs match archived
            identities.
          </p>
          {missingScope(draft) && (
            <p className="mt-2 text-xs text-destructive" role="alert">
              Select a query’s rule or upstream in its details to filter by the
              correct configuration. Results still show the previous selection.
            </p>
          )}
        </details>
      </form>
      <div className="my-[15px] flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground">
        <div className="flex flex-wrap items-center gap-2">
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
            variant="ghost"
            size="sm"
            className="text-xs"
            disabled={!liveAllowed}
            onClick={() => setLive(!live)}
          >
            {live ? "Pause live" : "Start live"}
          </Button>
        </div>
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
            compact={!technical}
            items={rows(state.data)}
            columns={[
              {
                key: "time",
                label: "Time",
                width: 84,
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
                width: technical ? 190 : "20%",
                render: (r) => (
                  <ClientIdentity
                    compact
                    source={
                      r.client_name_source
                        ? String(r.client_name_source)
                        : undefined
                    }
                    address={text(r.client)}
                    name={r.client_name ? String(r.client_name) : undefined}
                    device={r.client_device as Device | undefined}
                  />
                ),
              },
              {
                key: "name",
                label: "Domain",
                width: technical ? 260 : undefined,
                render: (r) => (
                  <div className="min-w-0 space-y-0.5">
                    <button
                      className="block w-full truncate border-0 bg-transparent p-0 text-left text-[14px] text-foreground hover:underline"
                      onClick={() => inspect(r)}
                      title={text(r.name)}
                    >
                      {text(r.name)}
                    </button>
                    <span className="block text-[12px] text-muted-foreground">
                      {text(r.qtype)}
                    </span>
                  </div>
                ),
              },
              {
                key: "outcome",
                label: "Result",
                width: 132,
                render: (r) => (
                  <div className="space-y-0.5">
                    <ResultBadge outcome={r.outcome} compact />
                    <div className="text-xs text-muted-foreground">
                      <ResponseTime value={r.duration_us} />
                    </div>
                  </div>
                ),
              },
              {
                key: "response",
                label: "Answer",
                width: technical ? 200 : "20%",
                render: (r) => (
                  <AnswerPreview row={r} inspect={() => inspect(r)} />
                ),
              },
              {
                key: "actions",
                label: "Action",
                width: 96,
                render: (r) => (
                  <InlineRuleAction
                    address={text(r.client)}
                    name={typeof r.name === "string" ? r.name : ""}
                    outcome={text(r.outcome)}
                  />
                ),
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
              {
                key: "generation",
                label: "Configuration version",
                hidden: !technical,
              },
            ]}
          />
        </section>
        <div className="my-[15px] flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground">
          <span>
            Page {count(cursors.length)} · up to {count(100)} queries
          </span>
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
            The DNS response and how this query was handled.
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
