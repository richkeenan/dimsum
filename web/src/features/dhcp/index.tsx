import { useState } from "react";
import {
  CircleCheck,
  CircleHelp,
  LoaderCircle,
  Power,
  TriangleAlert,
} from "lucide-react";
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
import { SetupSummary, setupComplete } from "./setup";

export const panel =
  "min-w-0 rounded-lg border border-border bg-background p-5 sm:p-6";
const networkFields = [
  [
    "interface",
    "Network interface",
    "eth0",
    "",
  ],
  [
    "server_ip",
    "Server IP address",
    "192.0.2.2",
    "Use a fixed IPv4 address.",
  ],
  [
    "gateway",
    "Router IP address",
    "192.0.2.1",
    "",
  ],
  [
    "subnet",
    "Subnet",
    "192.0.2.0/24",
    "",
  ],
] as const;
const topology = new Set(["interface", "server_ip", "subnet", "local_domain"]);
const leaseDurations = [
  [3600, "1 hour"],
  [43200, "12 hours"],
  [86400, "24 hours"],
  [604800, "7 days"],
] as const;
const selectClass =
  "h-11 w-full rounded-md border border-input bg-background px-3 text-sm disabled:cursor-not-allowed disabled:opacity-50";

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
        <details className="my-3 text-xs">
          <summary className="py-2 font-medium">Error details</summary>
          <Details value={error.fields} />
        </details>
      )}
      {error instanceof APIError &&
        (error.status === 0 || error.status >= 500) && (
          <p className="my-3 text-xs">
            Couldn’t confirm the save. Reload settings before trying again.
          </p>
        )}
    </>
  );
}

export function DHCPState({ value }: { value: DHCPStatusResponse }) {
  const d = value.runtime_available ? value.dhcp : null;
  const error = value.status.error || d?.last_error || d?.runtime.error;
  const paused = d?.runtime.clock_suspended;
  const attention = !!error || d?.state === "error" || d?.state === "degraded";
  const pending =
    value.status.pending ||
    d?.state === "starting" ||
    (!!d?.pending_generation && d.pending_generation !== "0") ||
    (!!d && d.desired_enabled !== d.applied_enabled);
  const title = !d
    ? "DHCP status unavailable"
    : paused
      ? "DHCP is paused"
      : attention
        ? "DHCP needs attention"
        : pending
          ? d.desired_enabled !== d.applied_enabled || d.state === "starting"
            ? d.desired_enabled
              ? "Starting DHCP…"
              : "Stopping DHCP…"
            : "Updating DHCP…"
          : d.applied_enabled
            ? "DHCP is on"
            : "DHCP is off";
  const Icon = !d
    ? CircleHelp
    : paused || attention
      ? TriangleAlert
      : pending
        ? LoaderCircle
        : d.applied_enabled
          ? CircleCheck
          : Power;
  const description = !d
    ? "Can’t read the server’s current status. Try refreshing the page."
    : paused
      ? "Check the server’s date and time to resume assigning addresses."
      : attention
        ? "Review the error below or run a setup check in Troubleshooting."
        : pending
          ? "Applying your saved settings."
          : d.applied_enabled
            ? `Assigning addresses on ${d.interface} (${d.server_ip}).`
            : "";
  return (
    <section
      className="flex items-start gap-3 px-1 py-1"
      aria-label="DHCP status"
    >
      <div
        className={`flex size-11 shrink-0 items-center justify-center rounded-full ${paused || attention ? "bg-destructive/10 text-destructive" : d?.applied_enabled || pending ? "bg-accent text-primary" : "bg-muted text-muted-foreground"}`}
      >
        <Icon aria-hidden="true" size={22} strokeWidth={1.5} />
      </div>
      <div className="min-w-0" aria-live="polite">
        <h2 className="text-lg font-semibold tracking-tight">{title}</h2>
        {description && (
          <p className="mt-1 text-xs text-muted-foreground wrap-anywhere">
            {description}
          </p>
        )}
        {error && (
          <p
            role="alert"
            className="mt-2 text-xs text-destructive wrap-anywhere"
          >
            {error}
          </p>
        )}
        {value.status.restart_required && (
          <p role="status" className="mt-2 text-xs">
            Restart dimsum to finish applying your network changes.
          </p>
        )}
      </div>
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
  const [needsReload, setNeedsReload] = useState(false);
  const [customLease, setCustomLease] = useState(false);
  const [editing, setEditing] = useState<boolean>();
  const blocked = busy || needsReload;
  const value = state.data;
  if (!value)
    return (
      <Resource state={state} retry={refresh}>
        {null}
      </Resource>
    );
  const proposed =
    !value.config.enabled && value.setup ? value.setup.config : value.config;
  const config = draft?.config ?? proposed;
  const hasSuggestions =
    !value.config.enabled && (value.setup?.suggested.length ?? 0) > 0;
  const canSave = !!draft || hasSuggestions;
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
  const customDuration =
    customLease ||
    (Number(config.lease_seconds) !== 0 &&
      !leaseDurations.some(
        ([seconds]) => seconds === Number(config.lease_seconds),
      ));
  const field = (
    key: keyof DHCPSettings,
    label: string,
    placeholder: string,
    help?: string,
  ) => (
    <div className="min-w-0" key={key}>
      <label htmlFor={`dhcp-${key}`} className="mb-2 block text-xs font-medium">
        {label}
      </label>
      <Input
        id={`dhcp-${key}`}
        className="h-11 placeholder:text-muted-foreground/60 placeholder:italic"
        placeholder={`e.g. ${placeholder}`}
        value={String(config[key] ?? "")}
        disabled={blocked || (locked && topology.has(key))}
        required={config.enabled && key !== "max_leases"}
        type={
          key === "lease_seconds" || key === "max_leases" ? "number" : "text"
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
        aria-describedby={help ? `dhcp-${key}-help` : undefined}
        onChange={(e) => change(key, e.target.value)}
      />
      {help && (
        <p
          id={`dhcp-${key}-help`}
          className="mt-1.5 text-[13px] text-muted-foreground"
        >
          {help}
        </p>
      )}
    </div>
  );
  return (
    <Resource state={state} retry={refresh}>
      <section className={panel}>
        <form
          onInvalidCapture={(e) => {
            // Reveal native validation targets before the browser focuses them.
            let details = (e.target as HTMLElement).closest("details");
            while (details) {
              details.open = true;
              details = details.parentElement?.closest("details") ?? null;
            }
            setEditing(true);
          }}
          onSubmit={async (e) => {
            e.preventDefault();
            if (!canSave || blocked || outdated) return;
            setBusy(true);
            setError(undefined);
            setSaved(undefined);
            try {
              const edits = Object.entries(config)
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
                revision: draft?.revision ?? value.status.saved_revision,
                edits,
              });
              setSaved(result);
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
          <div className="flex flex-col justify-between gap-4 border-b border-border pb-5 sm:flex-row sm:items-start">
            <div>
              <h2 className="text-lg font-semibold tracking-tight">
                Network settings
              </h2>
            </div>
            <label className="relative inline-flex min-h-11 shrink-0 cursor-pointer items-center gap-3 self-start text-xs font-medium">
              <input
                type="checkbox"
                role="switch"
                className="peer absolute inset-0 z-10 h-full w-full cursor-pointer opacity-0 disabled:cursor-not-allowed"
                checked={config.enabled}
                disabled={blocked}
                onChange={(e) => change("enabled", e.target.checked)}
              />
              <span
                aria-hidden="true"
                className="flex h-6 w-11 items-center rounded-full bg-muted-foreground/40 p-0.5 transition-colors duration-150 peer-checked:bg-primary peer-checked:[&>span]:translate-x-5 peer-focus-visible:outline-2 peer-focus-visible:outline-offset-4 peer-focus-visible:outline-ring peer-disabled:opacity-50"
              >
                <span className="size-5 rounded-full bg-white shadow-sm transition-transform duration-150" />
              </span>
              Enable DHCP
            </label>
          </div>
          <SetupSummary config={config} value={value} />
          <details
            className="border-t border-border"
            open={editing ?? !setupComplete(config)}
            onToggle={(e) => setEditing(e.currentTarget.open)}
          >
            <summary className="w-fit py-3 text-xs font-medium text-primary">
              Edit settings
            </summary>
            <fieldset className="mt-6 min-w-0">
              <legend className="mb-4 text-sm font-medium">Your network</legend>
              {locked && (
                <p className="mb-4 text-xs text-muted-foreground">
                  Save with DHCP off to change the interface, server address,
                  subnet or domain.
                </p>
              )}
              <div className="grid gap-x-6 gap-y-5 sm:grid-cols-2">
                {networkFields.map(([key, label, placeholder, help]) =>
                  field(key, label, placeholder, help),
                )}
              </div>
            </fieldset>
            <fieldset className="mt-7 min-w-0 border-t border-border pt-5">
              <legend className="pr-3 text-sm font-medium">
                Addresses for devices
              </legend>
              <p className="mb-4 text-xs text-muted-foreground">
                Choose a range that excludes your router, server and other fixed
                addresses.
              </p>
              <div className="grid gap-x-6 gap-y-5 sm:grid-cols-2">
                {field("range_start", "First IP address", "192.0.2.100")}
                {field("range_end", "Last IP address", "192.0.2.199")}
                <div>
                  <label
                    htmlFor="dhcp-duration"
                    className="mb-2 block text-xs font-medium"
                  >
                    Lease duration
                  </label>
                  <select
                    id="dhcp-duration"
                    className={selectClass}
                    value={
                      customDuration
                        ? "custom"
                        : String(config.lease_seconds || "")
                    }
                    disabled={blocked}
                    required={config.enabled}
                    onChange={(e) => {
                      setCustomLease(e.target.value === "custom");
                      if (e.target.value !== "custom")
                        change("lease_seconds", e.target.value || "0");
                    }}
                  >
                    <option value="">Choose duration</option>
                    {leaseDurations.map(([seconds, label]) => (
                      <option key={seconds} value={seconds}>
                        {label}
                      </option>
                    ))}
                    <option value="custom">Custom duration</option>
                  </select>
                  {customDuration && (
                    <div className="mt-3">
                      {field(
                        "lease_seconds",
                        "Custom duration (seconds)",
                        "86400",
                        "From 60 seconds to 7 days.",
                      )}
                    </div>
                  )}
                </div>
                {field(
                  "local_domain",
                  "Local domain",
                  "home.arpa",
                  "Used for device names, such as printer.home.arpa.",
                )}
              </div>
            </fieldset>
            <details className="mt-6 border-t border-border pt-2">
              <summary className="w-fit py-3 text-xs font-medium">
                Advanced settings
              </summary>
              <div className="max-w-sm pb-4 pt-1">
                {field(
                  "max_leases",
                  "Maximum leases",
                  "1024",
                  "Use 0 for the default of 1,024. Maximum: 4,096.",
                )}
              </div>
            </details>
          </details>
          {config.enabled && !value.config.enabled && (
            <p className="my-4 rounded-md bg-accent px-4 py-3 text-xs">
              Turn off DHCP on your router before enabling it here.
            </p>
          )}
          {outdated && (
            <p role="status" className="my-3 text-xs">
              Settings have changed elsewhere. Reload to use the latest
              settings.
            </p>
          )}
          {needsReload && (
            <p role="status" className="my-3 text-xs">
              {busy
                ? "Saved. Refreshing settings…"
                : "Settings saved, but couldn’t refresh. Reload settings before editing again."}
            </p>
          )}
          {error &&
            (needsReload ? (
              <ErrorNotice error={error} />
            ) : (
              <DHCPError error={error} />
            ))}
          <div className="mt-3 flex flex-wrap items-center gap-3 border-t border-border pt-5">
            <Button
              className="min-h-11"
              disabled={blocked || !canSave || outdated}
            >
              {busy
                ? "Saving…"
                : config.enabled !== value.config.enabled
                  ? config.enabled
                    ? "Save and enable"
                    : "Save and disable"
                  : "Save settings"}
            </Button>
            {(draft || error || needsReload) && (
              <Button
                type="button"
                variant="ghost"
                className="min-h-11"
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
                    setNeedsReload(false);
                    setError(undefined);
                    setSaved(undefined);
                    setCustomLease(false);
                  } catch (e) {
                    setError(e as Error);
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                Reload settings
              </Button>
            )}
            {canSave && !busy && !needsReload && !outdated && (
              <span className="text-xs text-muted-foreground">
                {draft
                  ? "Unsaved changes"
                  : "Suggested settings · not saved yet"}
              </span>
            )}
          </div>
          {saved && !needsReload && (
            <p role="status" className="mt-3 text-xs">
              Settings saved.{" "}
              {saved.error ? `Couldn’t apply changes: ${saved.error}` : ""}
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
    <div className="min-w-0 max-w-6xl space-y-6 [&_p]:leading-relaxed">
      <Resource state={status} retry={refresh}>
        {status.data && <DHCPState value={status.data} />}
      </Resource>
      <DHCPForm applied={status.data} refresh={refresh} tick={tick} />
      <Leases tick={tick} enabled={status.data?.dhcp?.applied_enabled} />
      <Reservations tick={tick} refresh={refresh} />
      <DHCPCheck />
    </div>
  );
}
