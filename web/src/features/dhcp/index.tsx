import { useState } from "react";
import {
  api,
  APIError,
  type Activation,
  type DHCPConfigResponse,
  type DHCPSettings,
  type DHCPStatusResponse,
} from "@/lib/api";
import { useResource } from "@/lib/hooks";
import { Details, ErrorNotice, Resource } from "@/components/data";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Reservations, Leases, DHCPCheck } from "./operations";

export const panel =
  "mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background p-5";
const fields = [
  ["interface", "LAN interface", "eth0"],
  ["server_ip", "Static server IPv4 address", "192.0.2.2"],
  ["subnet", "Subnet (CIDR)", "192.0.2.0/24"],
  ["gateway", "Router IPv4 address", "192.0.2.1"],
  ["range_start", "Pool start", "192.0.2.100"],
  ["range_end", "Pool end", "192.0.2.199"],
  ["local_domain", "Local domain", "home.arpa"],
  ["lease_seconds", "Lease duration (seconds)", "86400"],
  ["max_leases", "Lease-state capacity", "1024"],
] as const;
const topology = new Set(["interface", "server_ip", "subnet", "local_domain"]);

export function DHCPError({ error }: { error: Error }) {
  return (
    <>
      <ErrorNotice
        error={
          error instanceof APIError &&
          error.status === 409 &&
          error.code !== "revision_conflict"
            ? new Error(error.message)
            : error
        }
      />
      {error instanceof APIError && error.fields != null && (
        <Details value={error.fields} />
      )}
      {error instanceof APIError &&
        (error.status === 0 || error.status >= 500) && (
          <p className="my-3 text-xs">
            The outcome may be unknown. Inspect status and reload the saved
            revision before retrying this change.
          </p>
        )}
    </>
  );
}

export function DHCPState({ value }: { value: DHCPStatusResponse }) {
  const d = value.dhcp;
  return (
    <section className={panel} aria-label="DHCP runtime status">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-sm font-medium">Service status</h2>
        <span className="rounded-md bg-muted px-3 py-1 text-xs capitalize">
          {d?.state ?? "Runtime unavailable"}
        </span>
      </div>
      <dl className="mt-4 grid gap-4 text-xs sm:grid-cols-2 lg:grid-cols-4 [&_dt]:text-muted-foreground [&_dd]:mt-1 [&_dd]:break-all [&_dd]:tabular-nums">
        <div>
          <dt>Desired</dt>
          <dd>
            {d ? (d.desired_enabled ? "Enabled" : "Disabled") : "Unavailable"} ·
            generation {d?.desired_generation ?? "—"}
          </dd>
        </div>
        <div>
          <dt>Applied</dt>
          <dd>
            {d ? (d.applied_enabled ? "Enabled" : "Disabled") : "Unavailable"} ·
            generation {d?.applied_generation ?? "—"}
          </dd>
        </div>
        <div>
          <dt>Applied interface / address</dt>
          <dd>
            {d?.interface || "—"} / {d?.server_ip || "—"}
          </dd>
        </div>
        <div>
          <dt>Lease storage</dt>
          <dd>
            {d?.runtime.storage || "Unopened"} · {d?.runtime.held ?? "0"} held /{" "}
            {d?.runtime.capacity ?? "0"} capacity
          </dd>
        </div>
      </dl>
      {(value.status.pending ||
        (d?.pending_generation && d.pending_generation !== "0")) && (
        <p className="mt-4 text-xs" role="status">
          Applying saved changes… Pending generation{" "}
          {d?.pending_generation ?? value.status.active_generation}. DHCP may
          still be using the previous configuration.
        </p>
      )}
      {(value.status.error || d?.last_error || d?.runtime.error) && (
        <div
          role="alert"
          className="mt-4 space-y-2 text-xs text-destructive wrap-anywhere"
        >
          <p>{value.status.error || d?.last_error || d?.runtime.error}</p>
          <p>
            Review the saved settings and lease ownership below, then run the
            environment check. A failed change does not release existing leases.
          </p>
        </div>
      )}
      {d?.runtime.clock_suspended && (
        <p role="alert" className="mt-3 text-xs">
          Allocation is suspended. Restore a trustworthy host clock before
          retrying.
        </p>
      )}
      {value.status.restart_required && (
        <p role="status" className="mt-3 text-xs">
          Listener changes require a service restart.
        </p>
      )}
      <p className="mt-4 text-xs text-muted-foreground">
        Saving configuration does not confirm that DHCP is serving. DNS health
        is independent.
      </p>
    </section>
  );
}

export function DHCPForm({
  applied,
  refresh,
  tick = 0,
}: {
  applied?: DHCPStatusResponse;
  refresh: () => void;
  tick?: number;
}) {
  // Own the query alongside form state: releasing busy after refetch renders
  // the new cached document and revision, never stale props from a parent.
  const state = useResource<DHCPConfigResponse>("dhcp", tick);
  const [draft, setDraft] = useState<{
    revision: string;
    config: DHCPSettings;
  }>();
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState<Activation>();
  const value = state.data;
  if (!value)
    return (
      <Resource state={state} retry={refresh}>
        {null}
      </Resource>
    );
  const config = draft?.config ?? value.config;
  const outdated = !!draft && draft.revision !== value.status.saved_revision;
  // Saving disable is a separate boundary; unchecking the draft cannot unlock topology.
  const locked =
    value.config.enabled || !applied || applied.dhcp?.applied_enabled === true;
  const change = (key: string, v: string | boolean) => {
    setDraft({
      revision: draft?.revision ?? value.status.saved_revision,
      config: { ...config, [key]: v },
    });
    setSaved(undefined);
  };
  return (
    <Resource state={state} retry={refresh}>
      <section className={panel}>
        <h2 className="mb-3 text-sm font-medium">DHCPv4 settings</h2>
        <p className="mb-4 max-w-[80ch] text-xs text-muted-foreground">
          Assign IPv4 addresses on one LAN. DHCP is off by default. Choose a
          pool that excludes static devices and the router’s existing leases.
          DNS must listen on the server address or 0.0.0.0, port 53.
        </p>
        <form
          onSubmit={async (e) => {
            e.preventDefault();
            if (!draft) return;
            setBusy(true);
            setError(undefined);
            setSaved(undefined);
            try {
              const edits = Object.entries(draft.config)
                .filter(
                  ([key, v]) =>
                    key !== "reservations" &&
                    v !== value.config[key as keyof DHCPSettings],
                )
                .map(([key, v]) => {
                  if (key === "lease_seconds" || key === "max_leases") {
                    if (
                      String(v).trim() === "" ||
                      !Number.isSafeInteger(Number(v))
                    )
                      throw new Error(
                        `Enter a whole number for ${key === "lease_seconds" ? "lease duration" : "capacity"}.`,
                      );
                    v = Number(v);
                  }
                  return { path: [key], value: v };
                });
              if (!edits.length) throw new Error("No settings have changed.");
              const result = await api.send<Activation>("dhcp", "PATCH", {
                revision: draft.revision,
                edits,
              });
              setSaved(result);
              setDraft(undefined);
              refresh();
            } catch (e) {
              setError(e as Error);
            } finally {
              setBusy(false);
            }
          }}
        >
          <label className="flex min-h-11 items-center gap-3 text-sm">
            <input
              type="checkbox"
              checked={config.enabled}
              disabled={busy}
              onChange={(e) => change("enabled", e.target.checked)}
            />
            Enable DHCPv4
          </label>
          {locked && (
            <p className="my-3 text-xs text-muted-foreground">
              To change interface, server address, subnet or domain, save DHCP
              as disabled and wait for Applied to show Disabled first.
            </p>
          )}
          <div className="my-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {fields.map(([key, label, placeholder]) => (
              <label
                key={key}
                className="flex min-w-0 flex-col gap-1.5 text-xs"
              >
                {label}
                <Input
                  placeholder={placeholder}
                  value={config[key] ?? ""}
                  disabled={busy || (locked && topology.has(key))}
                  required={config.enabled && key !== "max_leases"}
                  type={
                    key === "lease_seconds" || key === "max_leases"
                      ? "number"
                      : "text"
                  }
                  min={
                    key === "lease_seconds"
                      ? config.enabled
                        ? 60
                        : 0
                      : key === "max_leases"
                        ? 0
                        : undefined
                  }
                  max={
                    key === "lease_seconds"
                      ? 604800
                      : key === "max_leases"
                        ? 4096
                        : undefined
                  }
                  onChange={(e) => change(key, e.target.value)}
                />
              </label>
            ))}
          </div>
          <p className="mb-4 text-xs text-muted-foreground">
            All network fields and lease duration are required to enable.
            Incomplete settings may be saved while disabled. Capacity 0 uses
            1,024; maximum 4,096. Use a local domain such as home.arpa, never
            .local.
          </p>
          {outdated && (
            <p role="status" className="my-3 text-xs">
              Configuration changed. Your draft is preserved; reload the saved
              revision before editing again.
            </p>
          )}
          {error && <DHCPError error={error} />}
          <div className="flex flex-wrap gap-2">
            <Button disabled={busy || !draft || outdated}>
              Save DHCP settings
            </Button>
            <Button
              type="button"
              variant="outline"
              disabled={busy}
              onClick={async () => {
                setBusy(true);
                try {
                  // Cancel any older poll and await installation in this query's
                  // cache. A failed reload must leave the existing draft intact.
                  await state.refetch({
                    cancelRefetch: true,
                    throwOnError: true,
                  });
                  // Reload is not an edit: the next actual change captures a
                  // revision, so later reservation saves cannot stale a clean form.
                  setDraft(undefined);
                  setError(undefined);
                  setSaved(undefined);
                } catch (e) {
                  setError(e as Error);
                } finally {
                  setBusy(false);
                }
              }}
            >
              Reload saved settings
            </Button>
          </div>
          {saved && (
            <p role="status" className="mt-3 text-xs">
              DHCP settings saved.{" "}
              {saved.error
                ? `Activation failed: ${saved.error}`
                : "Check Applied status before relying on this change."}
            </p>
          )}
        </form>
      </section>
    </Resource>
  );
}

export default function DHCP() {
  const [tick, setTick] = useState(0);
  const status = useResource<DHCPStatusResponse>("dhcp/status", tick);
  const refresh = () => setTick((t) => t + 1);
  return (
    <div className="min-w-0 [&_p]:leading-relaxed">
      <Resource state={status} retry={refresh}>
        {status.data && <DHCPState value={status.data} />}
      </Resource>
      <DHCPForm applied={status.data} refresh={refresh} tick={tick} />
      <Reservations tick={tick} refresh={refresh} />
      <Leases tick={tick} />
      <DHCPCheck />
    </div>
  );
}
