import { useEffect, useRef, useState } from "react";
import { ChevronRight } from "lucide-react";
import { api, collectionRows, count, rows, text, type Row } from "@/lib/api";
import { useResource } from "@/lib/hooks";
import { DataTable, Details, ErrorNotice } from "@/components/data";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import type { Schema } from "./clients/model";

export type ListToggle = { id: unknown; enabled: boolean };

function listLabel(row: Row) {
  if (row.label) return text(row.label);
  try {
    const url = new URL(String(row.url));
    return `${url.hostname}${url.pathname === "/" ? "" : url.pathname}`;
  } catch {
    return text(row.url);
  }
}

export function ListSubscriptions({
  data,
  disabled,
  pending,
  toggle,
  edit,
}: {
  data?: Row;
  disabled: boolean;
  pending?: ListToggle;
  toggle: (row: Row, enabled: boolean) => void;
  edit: (row: Row) => void;
}) {
  const catalog = useResource<Row>("catalog");
  const clients = useResource<Schema["ClientsResponse"]>("clients");
  const profiles = useResource<{ items: Schema["PolicyProfile"][] }>(
    "profiles",
  );
  const configured = collectionRows(data);
  const presets = rows(catalog.data);
  // Match by URL rather than ID: existing/custom subscriptions may use any ID.
  const items = [
    ...presets.flatMap((preset) => {
      const matches = configured.filter((row) => row.url === preset.url);
      return matches.length
        ? matches.map((row) => ({ ...preset, ...row }))
        : [{ ...preset, enabled: false }];
    }),
    ...configured.filter(
      (row) => !presets.some((preset) => preset.url === row.url),
    ),
  ].map((row) => ({
    ...row,
    id: `${row.__index === undefined ? "catalog" : "source"}:${row.id}`,
    sourceID: row.id,
    source:
      row.__index === undefined
        ? undefined
        : rows(data?.status, "sources").find((source) => source.id === row.id),
  }));
  return (
    <>
      {catalog.error && <ErrorNotice error={catalog.error} />}
      <p className="px-4 py-3 text-xs text-muted-foreground">
        Choose lists to use across your network. Lists download and update
        automatically. You can choose different lists in a profile or a device’s
        settings.
      </p>
      {catalog.loading && (
        <p className="px-4 py-3 text-xs text-muted-foreground">
          Loading available lists…
        </p>
      )}
      <DataTable
        items={items}
        columns={[
          {
            key: "enabled",
            label: "Use by default",
            render: (row) => (
              <label className="flex min-h-9 min-w-9 cursor-pointer items-center justify-center has-disabled:cursor-default">
                <input
                  type="checkbox"
                  className="size-4 accent-primary focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-ring disabled:opacity-50"
                  aria-label={listLabel(row)}
                  checked={
                    pending && pending.id === row.id
                      ? pending.enabled
                      : (row.default_apply ?? row.enabled) === true
                  }
                  disabled={
                    disabled ||
                    (row.available === false && row.enabled !== true)
                  }
                  onChange={(event) => toggle(row, event.target.checked)}
                />
              </label>
            ),
          },
          {
            key: "label",
            label: "List",
            render: (row) => (
              <div className="min-w-48 max-w-96 whitespace-normal">
                <div className="flex flex-wrap items-center gap-2 font-medium">
                  {listLabel(row)}
                  {row.category === "parental-control" && (
                    <Badge variant="destructive">Adult content</Badge>
                  )}
                </div>
                {!!row.description && (
                  <p className="mt-1 text-xs text-muted-foreground">
                    {text(row.description)}
                  </p>
                )}
                {row.available === false && (
                  <p className="mt-1 text-xs text-muted-foreground">
                    {text(row.unavailable_reason)}
                  </p>
                )}
              </div>
            ),
          },
          {
            key: "status",
            label: "Status",
            render: (row) => {
              const source = row.source as Row | undefined;
              const changing = pending && pending.id === row.id;
              const status = changing
                ? pending.enabled
                  ? "Downloading…"
                  : "Saving…"
                : row.enabled !== true
                  ? row.__index === undefined
                    ? row.available === false
                      ? "Unavailable"
                      : "Available"
                    : "Not downloaded"
                  : source?.error
                    ? source.usable === true
                      ? "Downloaded · update failed"
                      : "Download failed"
                    : source?.enabled === true && source.usable === true
                      ? "Downloaded"
                      : "Waiting for activation";
              return (
                <div className="max-w-72 whitespace-normal" aria-live="polite">
                  <span>{status}</span>
                  {row.enabled === true && !!source?.error && (
                    <p className="mt-1 text-xs text-destructive">
                      {text(source.error)}
                    </p>
                  )}
                </div>
              );
            },
          },
          {
            key: "exceptions",
            label: "Device & profile settings",
            render: (row) => {
              const id = String(row.sourceID);
              const devices =
                clients.data?.items?.filter(
                  (c) => c.overrides?.lists?.[id] !== undefined,
                ) ?? [];
              const owners =
                profiles.data?.items?.filter(
                  (p) => p.policy?.lists?.[id] !== undefined,
                ) ?? [];
              if (!devices.length && !owners.length)
                return <span className="text-muted-foreground">—</span>;
              return (
                <details className="max-w-64 whitespace-normal text-xs">
                  <summary className="cursor-pointer py-2">
                    {devices.length} devices · {owners.length} profiles
                  </summary>
                  {devices.map((c) => (
                    <a
                      className="block py-2 underline"
                      key={c.policy_id}
                      href={`/clients?device=${encodeURIComponent(c.policy_id!)}`}
                    >
                      {c.name || c.policy_id}:{" "}
                      {c.overrides!.lists![id] ? "On" : "Off"}
                    </a>
                  ))}
                  {owners.map((p) => (
                    <p key={p.id}>
                      {p.name || p.id}: {p.policy!.lists![id] ? "On" : "Off"} ·{" "}
                      {clients.data?.items?.filter((c) => c.profile === p.id)
                        .length ?? 0}{" "}
                      assigned devices
                    </p>
                  ))}
                  {(clients.error || profiles.error) && (
                    <span>Usage unavailable</span>
                  )}
                </details>
              );
            },
          },
          {
            key: "rules",
            label: "Domains",
            align: "right",
            render: (row) => count((row.source as Row | undefined)?.rules),
          },
          {
            key: "actions",
            label: "Actions",
            align: "right",
            render: (row) => (
              <div className="flex items-start justify-end gap-2">
                {row.__index !== undefined && (
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={disabled}
                    onClick={() =>
                      edit({
                        ...row,
                        id: row.sourceID,
                        __references:
                          (clients.data?.items?.filter(
                            (c) =>
                              c.overrides?.lists?.[String(row.sourceID)] !==
                              undefined,
                          ).length ?? 0) +
                          (profiles.data?.items?.filter(
                            (p) =>
                              p.policy?.lists?.[String(row.sourceID)] !==
                              undefined,
                          ).length ?? 0),
                      })
                    }
                  >
                    Edit
                  </Button>
                )}
                <details className="group min-w-0 text-left">
                  <Button asChild size="sm" variant="outline">
                    <summary className="cursor-pointer list-none [&::-webkit-details-marker]:hidden">
                      <ChevronRight
                        aria-hidden="true"
                        className="group-open:rotate-90"
                      />
                      Details
                    </summary>
                  </Button>
                  <Details
                    value={{
                      URL: row.url,
                      format: row.dialect,
                      scope: row.domain_kind,
                      ID: row.sourceID,
                      checksum: (row.source as Row | undefined)?.sha256,
                    }}
                  />
                </details>
              </div>
            ),
          },
        ]}
        empty="No blocklists available. Add a custom URL to get started."
      />
    </>
  );
}

export function RefreshListsButton({
  disabled,
  completed,
}: {
  disabled: boolean;
  completed: () => void;
}) {
  const [job, setJob] = useState<Row>();
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState<Error>();
  const [tick, setTick] = useState(0);
  const jobs = useResource<Row>("jobs", tick);
  const callback = useRef(completed);
  callback.current = completed;
  const current = rows(jobs.data).find((entry) => entry.id === job?.id);
  const observed = rows(jobs.data).find(
    (entry) => entry.kind === "refresh" && entry.state === "running",
  );
  const running = starting || job?.state === "running" || !!observed;
  useEffect(() => {
    if (!job && observed) {
      setJob(observed);
      return;
    }
    if (!job || !current || current.state === "running") return;
    setJob(undefined);
    if (current.state === "failed") setError(new Error(text(current.error)));
    callback.current();
  }, [job, current, observed]);
  async function refresh() {
    setStarting(true);
    setError(undefined);
    try {
      setJob(await api.send<Row>("jobs", "POST", { kind: "refresh" }));
      setTick((value) => value + 1);
    } catch (error) {
      setError(error as Error);
    } finally {
      setStarting(false);
    }
  }
  return (
    <div>
      <Button
        variant="outline"
        disabled={disabled || running}
        onClick={() => void refresh()}
      >
        {running ? "Updating blocklists…" : "Update blocklists"}
      </Button>
      {(error || (job && jobs.error)) && (
        <ErrorNotice error={error ?? jobs.error!} />
      )}
    </div>
  );
}
