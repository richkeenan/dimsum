import { useState } from "react";
import { api } from "@/lib/api";
import { ErrorNotice } from "@/components/data";
import { ActivationStatus } from "./policy";
import { selectClass, policyID, type ClientRow, type Schema } from "./model";

export function ProfileAssignment({
  row,
  profiles,
  reload,
  disabled = false,
  onStatus,
}: {
  row: ClientRow;
  profiles: Schema["PolicyProfile"][];
  reload: () => void | Promise<unknown>;
  disabled?: boolean;
  onStatus?: (status: Schema["Activation"]) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  const [status, setStatus] = useState<Schema["Activation"]>();
  const name =
    row.configured?.name ||
    row.observed[0]?.name ||
    row.observed[0]?.address ||
    row.key;
  return (
    <div className="min-w-0 space-y-1">
      <select
        aria-label={`Profile for ${name}`}
        className={`${selectClass} w-full min-h-9 py-1 text-xs`}
        value={row.configured?.profile ?? ""}
        disabled={disabled || busy}
        onChange={async (e) => {
          const profile = e.target.value;
          setBusy(true);
          setError(undefined);
          setStatus(undefined);
          try {
            const current = await api.get<Schema["ClientsResponse"]>("clients");
            const observed = current.observed?.items.find(
              (o) => o.address === row.observed[0]?.address,
            );
            const configured = current.items?.find(
              (c) =>
                c.policy_id ===
                (row.configured?.policy_id ?? observed?.client_id),
            );
            if (row.configured && !configured)
              throw new Error(
                "This device’s settings changed. Refresh before assigning a profile.",
              );
            if (!configured && !observed)
              throw new Error(
                "This device is no longer in the device list. Refresh and try again.",
              );
            const generatedID = policyID("device");
            const result = await api.send<Schema["Activation"]>(
              "client-policy",
              "PATCH",
              {
                revision: current.status.saved_revision,
                scope: "client",
                profile,
                ...(configured
                  ? {
                      id: configured.policy_id,
                      ...(!configured.id ? { promote_id: generatedID } : {}),
                    }
                  : {
                      id: generatedID,
                      create: true,
                      ...(observed!.name ? { name: observed!.name } : {}),
                      ...(observed!.authoritative_mac
                        ? { lease_address: observed!.address }
                        : { selectors: { addresses: [observed!.address] } }),
                    }),
              },
            );
            setStatus(result);
            onStatus?.(result);
            const readback = await reload();
            if (
              readback &&
              typeof readback === "object" &&
              "error" in readback &&
              readback.error
            ) {
              throw new Error(
                "Profile saved, but the device list could not be refreshed. Refresh to see the saved assignment.",
              );
            }
            if (
              readback &&
              typeof readback === "object" &&
              "data" in readback
            ) {
              const saved = readback.data as
                Schema["ClientsResponse"] | undefined;
              if (saved?.status) {
                setStatus(saved.status);
                onStatus?.(saved.status);
              }
            }
          } catch (e) {
            setError(e as Error);
          } finally {
            setBusy(false);
          }
        }}
      >
        <option value="">Network defaults</option>
        {profiles.map((p) => (
          <option key={p.id} value={p.id}>
            {p.name || p.id}
          </option>
        ))}
      </select>
      {busy && (
        <p role="status" className="text-xs text-muted-foreground">
          Saving…
        </p>
      )}
      {error && <ErrorNotice error={error} />}
      {status && !onStatus && <ActivationStatus status={status} />}
    </div>
  );
}

export function ProfileMap({
  rows,
  profiles,
  reload,
  disabled,
  onEdit,
  liveStatus,
}: {
  rows: ClientRow[];
  profiles: Schema["PolicyProfile"][];
  reload: () => void | Promise<unknown>;
  disabled?: boolean;
  onEdit: (id: string) => void;
  liveStatus?: Schema["Activation"];
}) {
  const [status, setStatus] = useState<Schema["Activation"]>();
  return (
    <section aria-label="Device profile assignments" className="space-y-4">
      <div>
        <h2 className="text-base font-medium">Devices & profiles</h2>
        <p className="mt-1 text-xs text-muted-foreground">
          Each device belongs to one group. Choose a profile below a device to
          move it.
        </p>
      </div>
      {status && (
        <ActivationStatus
          status={
            liveStatus?.saved_revision === status.saved_revision
              ? liveStatus
              : status
          }
        />
      )}
      <div className="grid items-start gap-4 md:grid-cols-2 xl:grid-cols-3">
        {[{ id: "", name: "Network defaults" }, ...profiles].map((profile) => {
          const members = rows.filter(
            (row) => (row.configured?.profile ?? "") === profile.id,
          );
          return (
            <section
              key={profile.id}
              aria-label={`${profile.name || profile.id} devices`}
              className="overflow-hidden rounded-lg border border-border bg-background"
            >
              <header className="flex items-center justify-between gap-3 border-b border-border bg-muted px-4 py-3">
                <h3 className="text-sm font-medium">
                  {profile.id ? (
                    <button
                      className="underline decoration-border underline-offset-4 hover:decoration-primary"
                      aria-label={`Edit ${profile.name || profile.id} profile`}
                      onClick={() => onEdit(profile.id)}
                    >
                      {profile.name || profile.id}
                    </button>
                  ) : (
                    profile.name
                  )}
                </h3>
                <span className="text-xs text-muted-foreground">
                  {members.length} {members.length === 1 ? "device" : "devices"}
                </span>
              </header>
              <ul className="m-4 max-h-96 space-y-3 overflow-y-auto border-l border-border pl-4">
                {members.map((row) => (
                  <li
                    key={row.key}
                    className="relative before:absolute before:top-3 before:-left-4 before:w-3 before:border-t before:border-border"
                  >
                    <p className="mb-1 break-words text-xs font-medium">
                      {row.configured?.name ||
                        row.observed[0]?.name ||
                        row.observed[0]?.address ||
                        row.key}
                    </p>
                    <ProfileAssignment
                      row={row}
                      profiles={profiles}
                      reload={reload}
                      disabled={disabled}
                      onStatus={setStatus}
                    />
                  </li>
                ))}
              </ul>
              {!members.length && (
                <p className="px-4 pb-4 text-xs text-muted-foreground">
                  No devices assigned yet.
                </p>
              )}
            </section>
          );
        })}
      </div>
    </section>
  );
}
