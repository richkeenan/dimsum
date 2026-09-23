import { useState } from "react";
import {
  api,
  APIError,
  type DHCPReservation,
  type DHCPReservationsResponse,
  type DHCPLeasesResponse,
  type Job,
} from "@/lib/api";
import { useResource } from "@/lib/hooks";
import { DataTable, Details, ErrorNotice, Resource } from "@/components/data";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { DHCPError, panel } from "./index";

export function Reservations({ tick, refresh }: { tick: number; refresh: () => void }) {
  const state = useResource<DHCPReservationsResponse>("dhcp/reservations", tick);
  const [draft, setDraft] = useState<{
    revision: string;
    item: DHCPReservation;
    existing: boolean;
    remove?: boolean;
  }>();
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  const [needsReload, setNeedsReload] = useState(false);
  const blocked = busy || needsReload;
  const [page, setPage] = useState(0);
  const items = state.data?.items ?? [];
  const currentPage = Math.min(page, Math.max(0, Math.ceil(items.length / 25) - 1));
  const outdated = !!draft && draft.revision !== state.data?.status.saved_revision;
  function open(item: DHCPReservation, existing: boolean, remove = false) {
    if (!state.data || blocked) return;
    setDraft({
      revision: state.data.status.saved_revision,
      item: { ...item },
      existing,
      remove,
    });
    setError(undefined);
  }
  return (
    <section
      className={`${panel} [&_button]:min-h-11 [&_input]:min-h-11`}
      aria-label="Reservations"
    >
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-sm font-medium">Reservations</h2>
        <Button
          variant="outline"
          disabled={!state.data || blocked || !!draft}
          onClick={() => open({ id: "", address: "", mac: "", hostname: "" }, false)}
        >
          Add reservation
        </Button>
      </div>
      <Resource state={state} retry={refresh}>
        {items.length === 0 ? (
          <p className="rounded-md bg-muted/50 px-4 py-5 text-xs text-muted-foreground">
            No reservations.
          </p>
        ) : (
          <DataTable
            items={items.slice(currentPage * 25, (currentPage + 1) * 25)}
            columns={[
              {
                key: "device",
                label: "Device",
                render: (r) => (
                  <div>
                    <p className="font-medium">{String(r.hostname || r.id)}</p>
                    <p className="text-xs text-muted-foreground">
                      {String(r.mac || "Identified by client ID")}
                    </p>
                    {r.client_id ? (
                      <details className="text-xs">
                        <summary className="min-h-11 cursor-pointer content-center">
                          Client ID
                        </summary>
                        <p className="max-w-64 whitespace-normal break-all">
                          {String(r.client_id)}
                        </p>
                      </details>
                    ) : null}
                  </div>
                ),
              },
              { key: "address", label: "IP address" },
              { key: "id", label: "Reservation name" },
              {
                key: "actions",
                label: "Actions",
                render: (r) => (
                  <div className="flex gap-2">
                    <Button
                      variant="outline"
                      disabled={blocked || !!draft}
                      aria-label={`Edit reservation ${r.id}`}
                      onClick={() => open(r as DHCPReservation, true)}
                    >
                      Edit
                    </Button>
                    <Button
                      variant="outline"
                      disabled={blocked || !!draft}
                      aria-label={`Remove reservation ${r.id}`}
                      onClick={() => open(r as DHCPReservation, true, true)}
                    >
                      Remove
                    </Button>
                  </div>
                ),
              },
            ]}
            empty="No reservations. Add one to keep a device at a fixed address."
          />
        )}
      </Resource>
      {items.length > 25 && (
        <div className="my-3 flex items-center gap-3 text-xs">
          <Button
            variant="outline"
            disabled={currentPage === 0}
            onClick={() => setPage(currentPage - 1)}
          >
            Previous reservations
          </Button>
          <span>Page {currentPage + 1}</span>
          <Button
            variant="outline"
            disabled={(currentPage + 1) * 25 >= items.length}
            onClick={() => setPage(currentPage + 1)}
          >
            Next reservations
          </Button>
        </div>
      )}
      {draft && (
        <form
          className="mt-4 border-t border-border pt-4"
          onSubmit={async (e) => {
            e.preventDefault();
            if (blocked || outdated) return;
            setBusy(true);
            setError(undefined);
            try {
              const path =
                "dhcp/reservations" +
                (draft.existing ? `/${encodeURIComponent(draft.item.id)}` : "");
              const body = draft.remove
                ? { revision: draft.revision }
                : draft.existing
                  ? {
                      revision: draft.revision,
                      edits: (["address", "mac", "client_id", "hostname"] as const).map((key) => ({
                        path: [key],
                        value: draft.item[key] ?? "",
                      })),
                    }
                  : { revision: draft.revision, item: draft.item };
              await api.send(
                path,
                draft.remove ? "DELETE" : draft.existing ? "PATCH" : "POST",
                body,
              );
              setNeedsReload(true);
              refresh();
              await state.refetch({ cancelRefetch: true, throwOnError: true });
              setDraft(undefined);
              setNeedsReload(false);
            } catch (e) {
              setError(e as Error);
            } finally {
              setBusy(false);
            }
          }}
        >
          <h3 className="mb-3 text-sm font-medium">
            {draft.remove
              ? `Remove reservation ${draft.item.id}?`
              : draft.existing
                ? "Edit reservation"
                : "New reservation"}
          </h3>
          {draft.remove ? (
            <p className="mb-4 text-xs">
              The device can keep its current address until its lease expires.
            </p>
          ) : (
            <div className="mb-4 grid gap-4 sm:grid-cols-2">
              {(
                [
                  ["id", "Reservation name"],
                  ["address", "IP address"],
                  ["hostname", "Hostname (optional)"],
                ] as const
              ).map(([key, label]) => (
                <div className="flex flex-col gap-1.5 text-xs" key={key}>
                  <label htmlFor={`reservation-${key}`}>{label}</label>
                  <Input
                    id={`reservation-${key}`}
                    aria-describedby={key === "id" ? "reservation-name-help" : undefined}
                    value={draft.item[key] ?? ""}
                    required={key !== "hostname"}
                    disabled={blocked || (key === "id" && draft.existing)}
                    onChange={(e) =>
                      setDraft({
                        ...draft,
                        item: { ...draft.item, [key]: e.target.value },
                      })
                    }
                  />
                  {key === "id" && (
                    <span id="reservation-name-help" className="text-muted-foreground">
                      Use a unique name with letters, numbers or hyphens, such as office-printer.
                    </span>
                  )}
                </div>
              ))}
              <label className="flex flex-col gap-1.5 text-xs">
                Identify device by
                <select
                  className="min-h-11 rounded-md border border-input bg-background px-3"
                  value={draft.item.client_id !== undefined ? "client_id" : "mac"}
                  disabled={blocked}
                  onChange={(e) =>
                    setDraft({
                      ...draft,
                      item: {
                        ...draft.item,
                        mac: e.target.value === "mac" ? "" : undefined,
                        client_id: e.target.value === "client_id" ? "" : undefined,
                      },
                    })
                  }
                >
                  <option value="mac">MAC address</option>
                  <option value="client_id">Client ID (advanced)</option>
                </select>
              </label>
              <label className="flex flex-col gap-1.5 text-xs">
                {draft.item.client_id !== undefined ? "Client ID (hex)" : "MAC address"}
                <Input
                  required
                  disabled={blocked}
                  value={draft.item.client_id ?? draft.item.mac ?? ""}
                  onChange={(e) =>
                    setDraft({
                      ...draft,
                      item: {
                        ...draft.item,
                        [draft.item.client_id !== undefined ? "client_id" : "mac"]: e.target.value,
                      },
                    })
                  }
                />
              </label>
            </div>
          )}
          {outdated && (
            <p role="status" className="mb-3 text-xs">
              Configuration changed. Cancel and reopen this form to use the latest revision.
            </p>
          )}
          {error && (needsReload ? <ErrorNotice error={error} /> : <DHCPError error={error} />)}
          <div className="flex gap-2">
            <Button disabled={blocked || outdated}>
              {draft.remove ? "Confirm removal" : "Save reservation"}
            </Button>
            <Button
              type="button"
              variant="outline"
              disabled={blocked}
              onClick={() => {
                setDraft(undefined);
                setError(undefined);
                refresh();
              }}
            >
              Cancel
            </Button>
          </div>
        </form>
      )}
      {needsReload && !busy && (
        <div className="mt-3 text-xs">
          <p role="status">
            Reservation change saved. Reload saved reservations before editing again.
          </p>
          <Button
            variant="outline"
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              try {
                await state.refetch({
                  cancelRefetch: true,
                  throwOnError: true,
                });
                setDraft(undefined);
                setNeedsReload(false);
                setError(undefined);
              } catch (e) {
                setError(e as Error);
              } finally {
                setBusy(false);
              }
            }}
          >
            Reload saved reservations
          </Button>
        </div>
      )}
    </section>
  );
}

function localTime(value: unknown) {
  if (typeof value !== "string" || !value || value.startsWith("0001-")) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? "—"
    : date.toLocaleString(undefined, {
        dateStyle: "medium",
        timeStyle: "short",
      });
}

function leaseState(value: unknown, expiry: unknown) {
  const state = String(value).toLowerCase();
  if (state === "bound") {
    const deadline = typeof expiry === "string" ? Date.parse(expiry) : NaN;
    return Number.isFinite(deadline)
      ? deadline > Date.now()
        ? "Active"
        : "Expired · address held"
      : "Lease expiry unavailable";
  }
  return (
    (
      {
        offered: "Offered to device",
        probing: "Checking address",
        "commit-pending": "Assigning address",
        quarantined: "Held after conflict",
      } as Record<string, string>
    )[state] ?? "Unknown"
  );
}

export function Leases({ tick, enabled }: { tick: number; enabled?: boolean }) {
  const [cursor, setCursor] = useState<string>();
  const [page, setPage] = useState(1);
  const [filter, setFilter] = useState("");
  const [refresh, setRefresh] = useState(0);
  const params = new URLSearchParams({ limit: "100" });
  if (filter) params.set("address", filter);
  if (cursor) params.set("cursor", cursor);
  const state = useResource<DHCPLeasesResponse>(`dhcp/leases?${params}`, tick + refresh);
  const expired = state.error instanceof APIError && state.error.code === "lease_cursor_expired";
  const restart = () => {
    setCursor(undefined);
    setPage(1);
    setRefresh((t) => t + 1);
  };
  if (enabled === false) {
    return (
      <section className={panel} aria-label="Leased addresses">
        <h2 className="text-sm font-medium">Leased addresses</h2>
        <p className="mt-2 text-xs text-muted-foreground">
          Turn on DHCP to see devices with assigned addresses.
        </p>
      </section>
    );
  }
  return (
    <section
      className={`${panel} [&_button]:min-h-11 [&_input]:min-h-11`}
      aria-label="Leased addresses"
    >
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-sm font-medium">Leased addresses</h2>
        <Button variant="outline" onClick={restart}>
          Refresh addresses
        </Button>
      </div>
      <label className="mb-4 flex max-w-80 flex-col gap-1.5 text-xs">
        Filter by IP address
        <Input
          value={filter}
          placeholder="192.0.2.100"
          onChange={(e) => {
            setFilter(e.target.value);
            setCursor(undefined);
            setPage(1);
          }}
        />
      </label>
      {expired ? (
        <div role="alert" className="my-3 rounded-md bg-muted p-4 text-xs">
          <p>The address list changed. Refresh to load the latest devices.</p>
          <Button className="mt-3" variant="outline" onClick={restart}>
            Reload address list
          </Button>
        </div>
      ) : state.error ? (
        <DHCPError error={state.error} />
      ) : state.isPlaceholderData ? (
        <p role="status">Loading addresses…</p>
      ) : (
        <Resource state={state} retry={restart}>
          {state.data && !state.data.runtime_available && (
            <p role="status" className="mb-4 text-xs">
              The live address list is unavailable. Devices may still have unexpired leases.
            </p>
          )}
          <DataTable
            items={state.data?.items ?? []}
            columns={[
              {
                key: "device",
                label: "Device",
                render: (r) => (
                  <div>
                    <p className="font-medium">{String(r.hostname || r.mac || "Unknown device")}</p>
                    {r.hostname ? (
                      <p className="text-xs text-muted-foreground">{String(r.mac || "")}</p>
                    ) : null}
                  </div>
                ),
              },
              { key: "address", label: "IP address" },
              {
                key: "state",
                label: "Status",
                render: (r) => leaseState(r.state, r.expiry),
              },
              {
                key: "expiry",
                label: "Expires",
                render: (r) => localTime(r.expiry),
              },
              {
                key: "details",
                label: "Details",
                render: (r) => (
                  <details className="text-xs">
                    <summary
                      className="min-h-11 cursor-pointer content-center"
                      aria-label={`Address details for ${r.address}`}
                    >
                      Details
                    </summary>
                    <dl className="max-w-64 space-y-2 whitespace-normal break-words py-2">
                      <div>
                        <dt className="text-muted-foreground">Address held until</dt>
                        <dd>{localTime(r.hold_until)}</dd>
                      </div>
                      <div>
                        <dt className="sr-only">About this hold</dt>
                        <dd className="text-muted-foreground">
                          The address stays set aside until this time, even if the lease has
                          expired.
                        </dd>
                      </div>
                      {r.client_id ? (
                        <div>
                          <dt className="text-muted-foreground">Client ID</dt>
                          <dd className="break-all">{String(r.client_id)}</dd>
                        </div>
                      ) : null}
                    </dl>
                  </details>
                ),
              },
            ]}
            empty={
              state.data?.runtime_available === false
                ? "No live address list available."
                : filter
                  ? "No devices match this IP address."
                  : "No leased addresses yet. Devices will appear when they request an address."
            }
          />
        </Resource>
      )}
      {(page > 1 || state.data?.next_cursor) && (
        <div className="mt-4 flex flex-wrap items-center gap-3 text-xs">
          <span>Page {page}</span>
          <Button variant="outline" disabled={page === 1 || state.isFetching} onClick={restart}>
            First page
          </Button>
          <Button
            variant="outline"
            disabled={state.isFetching || !!state.error || !state.data?.next_cursor}
            onClick={() => {
              setCursor(state.data?.next_cursor);
              setPage((p) => p + 1);
            }}
          >
            Next addresses
          </Button>
        </div>
      )}
    </section>
  );
}

export function DHCPCheck() {
  const [probe, setProbe] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  const [started, setStarted] = useState<Job>();
  const [tick, setTick] = useState(0);
  const jobs = useResource<{ items: Job[] }>("jobs", tick);
  const latest = jobs.data?.items.find((j) => j.id === started?.id) ?? started;
  return (
    <details id="dhcp-check" className={`${panel} [&_button]:min-h-11`}>
      <summary className="min-h-11 cursor-pointer content-center text-sm font-medium">
        Troubleshooting
      </summary>
      <label className="mb-3 flex min-h-11 items-center gap-3 text-xs">
        <input
          type="checkbox"
          className="size-4 accent-primary"
          aria-describedby={probe ? "dhcp-probe-help" : undefined}
          checked={probe}
          disabled={busy || latest?.state === "running"}
          onChange={(e) => setProbe(e.target.checked)}
        />
        Look for other DHCP servers
      </label>
      {probe && (
        <p id="dhcp-probe-help" className="mb-4 max-w-prose text-xs text-muted-foreground">
          Sends a discovery message on your network without requesting an address.
        </p>
      )}
      <Button
        disabled={
          busy || latest?.state === "running" || jobs.data?.items.some((j) => j.state === "running")
        }
        onClick={async () => {
          setBusy(true);
          setError(undefined);
          try {
            setStarted(
              await api.send<Job>("jobs", "POST", {
                kind: "dhcp-check",
                input: { probe_other_servers: probe, timeout_ms: 1000 },
              }),
            );
            setTick((t) => t + 1);
          } catch (e) {
            setError(e as Error);
          } finally {
            setBusy(false);
          }
        }}
      >
        {busy || latest?.state === "running" ? "Checking setup…" : "Check setup"}
      </Button>
      {error && <DHCPError error={error} />}
      {jobs.error && <DHCPError error={jobs.error} />}
      {latest && (
        <div className="mt-4 text-xs">
          <p role={latest.state === "failed" ? "alert" : "status"}>
            {latest.state === "running"
              ? "Checking setup…"
              : latest.state === "failed"
                ? "Setup check failed"
                : "Setup check finished"}
            {latest.error ? ` — ${latest.error}` : ""}
          </p>
          {latest.result != null && <CheckResults value={latest.result} />}
        </div>
      )}
    </details>
  );
}

function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function CheckResults({ value }: { value: unknown }) {
  const result = record(value);
  const checks = record(result.checks);
  const rows = [
    ["configuration", "Network settings", { valid: "Valid" }],
    [
      "dns_listener",
      "DNS listener",
      {
        "bound configuration matches; firewall/LAN reachability not proven":
          "Matches the configured address",
      },
    ],
    [
      "interface_static_address_socket",
      "Network interface and permissions",
      {
        "ready at check time": "Available at check time",
        "runtime-owned; inspect DHCP status and storage health":
          "In use by DHCP — see service status",
      },
    ],
    ["dns_ready", "DNS service", { true: "Ready", false: "Not ready" }],
  ] as const;
  const servers = Array.isArray(result.other_servers)
    ? result.other_servers.filter((server): server is string => typeof server === "string")
    : [];
  return (
    <div className="mt-3 space-y-3">
      <dl className="divide-y divide-border">
        {rows.map(([key, label, labels]) => {
          const raw = checks[key];
          const message =
            typeof raw === "string" && raw
              ? ((labels as Record<string, string>)[raw] ?? raw)
              : "Not available";
          return (
            <div key={key} className="grid gap-1 py-3 sm:grid-cols-2 sm:gap-4">
              <dt className="text-muted-foreground">{label}</dt>
              <dd className="whitespace-pre-wrap break-words">{message}</dd>
            </div>
          );
        })}
      </dl>
      {result.probe_requested === true && (
        <div className="rounded-md bg-muted p-3">
          <h3 className="font-medium">Other DHCP servers</h3>
          <p className="mt-1">
            {result.observation === "offers_observed"
              ? "Other servers responded:"
              : result.observation === "no_offer_observed"
                ? "No other servers responded during this check. A quiet server may still be present."
                : result.observation === "failed"
                  ? "The search could not finish."
                  : "No search result available."}
          </p>
          {servers.length > 0 && (
            <ul className="mt-2 list-inside list-disc">
              {servers.map((server) => (
                <li key={server}>{server}</li>
              ))}
            </ul>
          )}
          {result.probe_truncated === true && (
            <p className="mt-2">The search reached its limit; there may be more servers.</p>
          )}
        </div>
      )}
      <details>
        <summary className="min-h-11 cursor-pointer content-center text-muted-foreground">
          Technical details
        </summary>
        <Details value={value} />
      </details>
    </div>
  );
}
