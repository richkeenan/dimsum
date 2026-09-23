import { useState } from "react";
import { GripVertical } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { ErrorNotice } from "@/components/data";
import { ActivationStatus } from "./policy";
import { selectClass, policyID, type ClientRow, type Schema } from "./model";

function useAssignment(
  reload: () => void | Promise<unknown>,
  onStatus?: (status: Schema["Activation"]) => void,
) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  const [status, setStatus] = useState<Schema["Activation"]>();
  const save = async (row: ClientRow, profile: string) => {
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
          c.policy_id === (row.configured?.policy_id ?? observed?.client_id),
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
      if (readback && typeof readback === "object" && "data" in readback) {
        const saved = readback.data as Schema["ClientsResponse"] | undefined;
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
  };
  return { busy, error, status, save };
}

function deviceName(row: ClientRow) {
  return (
    row.configured?.name ||
    row.observed.find((o) => o.name)?.name ||
    row.observed[0]?.address ||
    row.configured?.address ||
    row.configured?.selectors?.addresses?.[0] ||
    row.key
  );
}

export function ProfileAssignment({
  row,
  profiles,
  reload,
  disabled = false,
  onStatus,
  assign,
}: {
  row: ClientRow;
  profiles: Schema["PolicyProfile"][];
  reload: () => void | Promise<unknown>;
  disabled?: boolean;
  onStatus?: (status: Schema["Activation"]) => void;
  assign?: (row: ClientRow, profile: string) => Promise<void>;
}) {
  const { busy, error, status, save } = useAssignment(reload, onStatus);
  return (
    <div className="min-w-0 space-y-1">
      <select
        aria-label={`Profile for ${deviceName(row)}`}
        className={`${selectClass} w-full min-h-9 py-1 text-xs`}
        value={row.configured?.profile ?? ""}
        disabled={disabled || busy}
        onChange={(e) => void (assign ?? save)(row, e.target.value)}
      >
        <option value="">Network defaults</option>
        {profiles.map((p) => (
          <option key={p.id} value={p.id}>
            {p.name || p.id}
          </option>
        ))}
      </select>
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
  const { status, error, busy, save } = useAssignment(reload);
  const [query, setQuery] = useState("");
  const [dragged, setDragged] = useState<string>();
  const [over, setOver] = useState<string>();
  const locked = disabled || busy;
  const compare = new Intl.Collator(undefined, {
    numeric: true,
    sensitivity: "base",
  });
  const sorted = [...rows].sort((a, b) => {
    const named = (r: ClientRow) =>
      !!(r.configured?.name || r.observed.some((o) => o.name));
    return (
      Number(named(b)) - Number(named(a)) ||
      compare.compare(deviceName(a), deviceName(b)) ||
      compare.compare(a.key, b.key)
    );
  });
  const filter = query.trim().toLocaleLowerCase();
  return (
    <section aria-label="Device profile assignments" className="space-y-4">
      <div>
        <h2 className="text-base font-medium">Devices & profiles</h2>
        <p className="mt-1 text-xs text-muted-foreground">
          Drag a device into a profile, or use its profile selector. Devices are
          sorted A–Z, followed by unnamed addresses.
        </p>
      </div>
      <div className="flex items-center gap-2">
        <Input
          type="search"
          aria-label="Find device"
          placeholder="Find device by name or address…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className="max-w-md"
        />
        {query && (
          <Button variant="ghost" onClick={() => setQuery("")}>
            Clear filter
          </Button>
        )}
      </div>
      {error && <ErrorNotice error={error} />}
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
        {[
          { id: "", name: "Network defaults" },
          ...[...profiles].sort((a, b) =>
            compare.compare(a.name || a.id, b.name || b.id),
          ),
        ].map((profile) => {
          const members = sorted.filter(
            (row) => (row.configured?.profile ?? "") === profile.id,
          );
          const visible = members.filter((row) =>
            [
              deviceName(row),
              row.configured?.address,
              ...(row.configured?.selectors?.addresses ?? []),
              ...row.observed.map((o) => o.address),
            ].some((value) => value?.toLocaleLowerCase().includes(filter)),
          );
          const canDrop =
            !locked &&
            dragged !== undefined &&
            rows.some(
              (row) =>
                row.key === dragged &&
                (row.configured?.profile ?? "") !== profile.id,
            );
          return (
            <section
              key={profile.id}
              aria-label={`${profile.name || profile.id} devices`}
              className={`overflow-hidden rounded-lg border bg-background ${over === profile.id && canDrop ? "border-primary ring-2 ring-primary/30" : "border-border"}`}
              onDragOver={(e) => {
                if (canDrop) {
                  e.preventDefault();
                  e.dataTransfer.dropEffect = "move";
                  setOver(profile.id);
                }
              }}
              onDragLeave={(e) => {
                if (!e.currentTarget.contains(e.relatedTarget as Node | null))
                  setOver(undefined);
              }}
              onDrop={(e) => {
                e.preventDefault();
                const row = rows.find((r) => r.key === dragged);
                setDragged(undefined);
                setOver(undefined);
                if (canDrop && row) void save(row, profile.id);
              }}
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
                  {filter
                    ? `${visible.length} of ${members.length} devices`
                    : `${members.length} ${members.length === 1 ? "device" : "devices"}`}
                </span>
              </header>
              <ul className="m-4 max-h-96 space-y-3 overflow-y-auto border-l border-border pl-4">
                {visible.map((row) => (
                  <li
                    key={row.key}
                    className="relative before:absolute before:top-3 before:-left-4 before:w-3 before:border-t before:border-border"
                  >
                    <div className="mb-1 flex items-center gap-1">
                      <span
                        draggable={!locked}
                        title={`Drag ${deviceName(row)} to a profile`}
                        aria-hidden="true"
                        className={`inline-flex min-h-9 items-center px-1 text-muted-foreground ${locked ? "opacity-40" : "cursor-grab active:cursor-grabbing"}`}
                        onDragStart={(e) => {
                          if (locked) {
                            e.preventDefault();
                            return;
                          }
                          e.dataTransfer.setData("text/plain", row.key);
                          e.dataTransfer.effectAllowed = "move";
                          setDragged(row.key);
                        }}
                        onDragEnd={() => {
                          setDragged(undefined);
                          setOver(undefined);
                        }}
                      >
                        <GripVertical className="size-4" />
                      </span>
                      <p className="break-words text-xs font-medium">
                        {deviceName(row)}
                      </p>
                    </div>
                    <ProfileAssignment
                      row={row}
                      profiles={profiles}
                      reload={reload}
                      disabled={locked}
                      assign={save}
                    />
                  </li>
                ))}
              </ul>
              {!visible.length && (
                <p className="px-4 pb-4 text-xs text-muted-foreground">
                  {members.length
                    ? "No matching devices."
                    : "No devices assigned yet. Drop a device here."}
                </p>
              )}
            </section>
          );
        })}
      </div>
    </section>
  );
}
