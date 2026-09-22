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

export function Reservations({
  tick,
  refresh,
}: {
  tick: number;
  refresh: () => void;
}) {
  const state = useResource<DHCPReservationsResponse>(
    "dhcp/reservations",
    tick,
  );
  const [draft, setDraft] = useState<{
    revision: string;
    item: DHCPReservation;
    existing: boolean;
    remove?: boolean;
  }>();
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [needsReload, setNeedsReload] = useState(false);
  const blocked = busy || needsReload;
  const [page, setPage] = useState(0);
  const items = state.data?.items ?? [];
  const currentPage = Math.min(
    page,
    Math.max(0, Math.ceil(items.length / 25) - 1),
  );
  const outdated =
    !!draft && draft.revision !== state.data?.status.saved_revision;
  function open(item: DHCPReservation, existing: boolean, remove = false) {
    if (!state.data || blocked) return;
    setDraft({
      revision: state.data.status.saved_revision,
      item: { ...item },
      existing,
      remove,
    });
    setError(undefined);
    setSaved(false);
  }
  return (
    <section className={panel} aria-label="Reservations">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-sm font-medium">Reservations</h2>
        <Button
          variant="outline"
          disabled={!state.data || blocked || !!draft}
          onClick={() =>
            open({ id: "", address: "", mac: "", hostname: "" }, false)
          }
        >
          Add reservation
        </Button>
      </div>
      <p className="mb-4 text-xs text-muted-foreground">
        Reserve an address inside the subnet for exactly one MAC or client ID.
        Editing or removing a reservation never revokes an existing lease.
      </p>
      <Resource state={state} retry={refresh}>
        <DataTable
          items={items.slice(currentPage * 25, (currentPage + 1) * 25)}
          columns={[
            { key: "id", label: "ID" },
            { key: "address", label: "Address" },
            { key: "hostname", label: "Hostname" },
            {
              key: "identity",
              label: "Identity",
              render: (r) =>
                String(
                  r.client_id ? `Client ID ${r.client_id}` : (r.mac ?? "—"),
                ),
            },
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
                      edits: (
                        ["address", "mac", "client_id", "hostname"] as const
                      ).map((key) => ({
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
              setSaved(true);
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
              Only the reservation is removed. Lease ownership and expiry remain
              intact.
            </p>
          ) : (
            <div className="mb-4 grid gap-4 sm:grid-cols-2">
              {(
                [
                  ["id", "Reservation ID"],
                  ["address", "Reserved IPv4 address"],
                  ["hostname", "Hostname (optional)"],
                ] as const
              ).map(([key, label]) => (
                <label className="flex flex-col gap-1.5 text-xs" key={key}>
                  {label}
                  <Input
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
                </label>
              ))}
              <label className="flex flex-col gap-1.5 text-xs">
                Identity type
                <select
                  className="min-h-9 rounded-md border border-input bg-background px-3"
                  value={
                    draft.item.client_id !== undefined ? "client_id" : "mac"
                  }
                  disabled={blocked}
                  onChange={(e) =>
                    setDraft({
                      ...draft,
                      item: {
                        ...draft.item,
                        mac: e.target.value === "mac" ? "" : undefined,
                        client_id:
                          e.target.value === "client_id" ? "" : undefined,
                      },
                    })
                  }
                >
                  <option value="mac">MAC address</option>
                  <option value="client_id">Client ID (hex)</option>
                </select>
              </label>
              <label className="flex flex-col gap-1.5 text-xs">
                {draft.item.client_id !== undefined
                  ? "Client ID (hex)"
                  : "MAC address"}
                <Input
                  required
                  disabled={blocked}
                  value={draft.item.client_id ?? draft.item.mac ?? ""}
                  onChange={(e) =>
                    setDraft({
                      ...draft,
                      item: {
                        ...draft.item,
                        [draft.item.client_id !== undefined
                          ? "client_id"
                          : "mac"]: e.target.value,
                      },
                    })
                  }
                />
              </label>
            </div>
          )}
          {outdated && (
            <p role="status" className="mb-3 text-xs">
              Configuration changed. Cancel and reopen this form to use the
              latest revision.
            </p>
          )}
          {error &&
            (needsReload ? (
              <ErrorNotice error={error} />
            ) : (
              <DHCPError error={error} />
            ))}
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
      {needsReload && (
        <div className="mt-3 text-xs">
          <p role="status">
            {busy
              ? "Reservation change saved. Refreshing saved reservations…"
              : "Reservation change saved. Reload saved reservations before editing again."}
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
      {saved && (
        <p role="status" className="mt-3 text-xs">
          Reservation change saved. Check Applied status for ownership
          conflicts.
        </p>
      )}
    </section>
  );
}

export function Leases({ tick }: { tick: number }) {
  const [cursor, setCursor] = useState<string>();
  const [page, setPage] = useState(1);
  const [filter, setFilter] = useState("");
  const [refresh, setRefresh] = useState(0);
  const params = new URLSearchParams({ limit: "100" });
  if (filter) params.set("address", filter);
  if (cursor) params.set("cursor", cursor);
  const state = useResource<DHCPLeasesResponse>(
    `dhcp/leases?${params}`,
    tick + refresh,
  );
  const expired =
    state.error instanceof APIError &&
    state.error.code === "lease_cursor_expired";
  const restart = () => {
    setCursor(undefined);
    setPage(1);
    setRefresh((t) => t + 1);
  };
  return (
    <section className={panel} aria-label="Leases">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-sm font-medium">Leases & ownership</h2>
        <Button variant="outline" onClick={restart}>
          Refresh leases
        </Button>
      </div>
      <p className="mb-4 text-xs text-muted-foreground">
        Expiry is the advertised grant deadline; held until is the conservative
        ownership deadline. There is no force-release operation.
      </p>
      <label className="mb-4 flex max-w-80 flex-col gap-1.5 text-xs">
        Filter lease IPv4 address
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
          <p>
            Lease ownership changed or the service restarted. Restart pagination
            to read a consistent page.
          </p>
          <Button className="mt-3" variant="outline" onClick={restart}>
            Restart lease pagination
          </Button>
        </div>
      ) : state.error ? (
        <DHCPError error={state.error} />
      ) : state.isPlaceholderData ? (
        <p role="status">Loading lease page…</p>
      ) : (
        <Resource state={state} retry={restart}>
          {state.data && !state.data.runtime_available && (
            <p role="status" className="mb-4 text-xs">
              Live lease inspection is unavailable. Disabled or stopped DHCP may
              still have preserved ownership on disk; an empty table does not
              mean those addresses are free.
            </p>
          )}
          <DataTable
            items={state.data?.items ?? []}
            columns={[
              { key: "address", label: "Address" },
              { key: "hostname", label: "Hostname" },
              { key: "state", label: "State" },
              {
                key: "identity",
                label: "Owner",
                render: (r) =>
                  String(
                    r.client_id ? `Client ID ${r.client_id} · ${r.mac}` : r.mac,
                  ),
              },
              { key: "expiry", label: "Expiry (UTC)" },
              { key: "hold_until", label: "Held until (UTC)" },
            ]}
            empty="No live leases match this selection."
          />
        </Resource>
      )}
      <div className="mt-4 flex flex-wrap items-center gap-3 text-xs">
        <span>Page {page} · up to 100 leases</span>
        <Button
          variant="outline"
          disabled={page === 1 || state.isFetching}
          onClick={restart}
        >
          First lease page
        </Button>
        <Button
          variant="outline"
          disabled={
            state.isFetching || !!state.error || !state.data?.next_cursor
          }
          onClick={() => {
            setCursor(state.data?.next_cursor);
            setPage((p) => p + 1);
          }}
        >
          Next leases
        </Button>
      </div>
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
    <section className={panel}>
      <h2 className="mb-3 text-sm font-medium">Environment check</h2>
      <p className="mb-3 text-xs text-muted-foreground">
        Check saved network settings, static address, DNS and socket
        permissions. This does not enable DHCP or prove firewall and LAN
        reachability.
      </p>
      <label className="mb-3 flex min-h-11 items-center gap-3 text-xs">
        <input
          type="checkbox"
          checked={probe}
          disabled={busy || latest?.state === "running"}
          onChange={(e) => setProbe(e.target.checked)}
        />
        Also send one DHCP discovery to look for other servers
      </label>
      <p className="mb-4 text-xs text-muted-foreground">
        The optional probe sends no lease request. No offer observed is not
        proof that another server or a sleeping static device is absent.
      </p>
      <Button
        disabled={
          busy ||
          latest?.state === "running" ||
          jobs.data?.items.some((j) => j.state === "running")
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
        Run DHCP check
      </Button>
      {error && <DHCPError error={error} />}
      {jobs.error && <DHCPError error={jobs.error} />}
      {latest && (
        <div className="mt-4 text-xs">
          <p role={latest.state === "failed" ? "alert" : "status"}>
            DHCP check: {latest.state}
            {latest.error ? ` — ${latest.error}` : ""}
          </p>
          {latest.result != null && <Details value={latest.result} />}
        </div>
      )}
    </section>
  );
}
