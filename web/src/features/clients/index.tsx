import { useEffect, useState } from "react";
import { api, count, APIError } from "@/lib/api";
import { useResource } from "@/lib/hooks";
import { ErrorNotice, Resource } from "@/components/data";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ActivationStatus, PolicyEditor } from "./policy";
import {
  mergeClients,
  panelClass,
  selectClass,
  words,
  type ClientRow,
  type Schema,
  type PolicyScope,
} from "./model";
import { DomainInspector } from "./inspect";
import {
  ClientIdentity,
  ClientDeviceButton,
} from "@/components/client-identity";

export default function Clients({
  range,
  selected,
  onSelect,
}: {
  range: string;
  selected?: string;
  onSelect: (id?: string) => void;
}) {
  const state = useResource<Schema["ClientsResponse"]>(
    `clients?${range}&limit=200`,
  );
  const [search, setSearch] = useState("");
  const [creating, setCreating] = useState<ClientRow | true>();
  const rows = mergeClients(state.data ?? {}).filter((row) =>
    JSON.stringify(row).toLowerCase().includes(search.toLowerCase()),
  );
  if (selected)
    return (
      <div className="space-y-4">
        <Button variant="ghost" onClick={() => onSelect()}>
          Back to devices
        </Button>
        <PolicyEditor
          scope="client"
          id={selected}
          onPromoted={onSelect}
          onDeleted={() => {
            onSelect();
            void state.reload();
          }}
        />
        <DomainInspector key={selected} clientID={selected} />
      </div>
    );
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <Input
          aria-label="Search devices"
          placeholder="Search name, identity or address"
          className="max-w-sm"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <Button onClick={() => setCreating(true)}>Add device</Button>
      </div>
      {creating && (
        <CreateOwner
          scope="client"
          observed={creating === true ? undefined : creating.observed[0]}
          revision={state.data?.status.saved_revision ?? ""}
          cancel={() => setCreating(undefined)}
          saved={(id) => {
            setCreating(undefined);
            void state.reload();
            onSelect(id);
          }}
        />
      )}
      <Resource state={state} retry={() => void state.reload()}>
        {state.data?.configuration_error && (
          <ErrorNotice error={new Error(state.data.configuration_error)} />
        )}
        {!state.data?.observed_available && (
          <p className="text-xs text-muted-foreground">
            Observations unavailable. Configured identities are still shown.
          </p>
        )}
        {state.data?.observed?.truncated && (
          <p className="text-xs text-muted-foreground">
            Showing the first 200 observed addresses. Narrow the history window
            to see others.
          </p>
        )}
        <div className="divide-y divide-border rounded-lg border border-border bg-background">
          {rows.map((row) => (
            <DeviceRow
              key={row.key}
              row={row}
              summary={state.data?.policy_summaries?.[row.key]}
              select={() =>
                row.configured ? onSelect(row.key) : setCreating(row)
              }
            />
          ))}
          {!rows.length && (
            <p className="p-5 text-sm text-muted-foreground">
              No devices found.
            </p>
          )}
        </div>
      </Resource>
      <a
        href="/profiles"
        className="inline-flex min-h-10 items-center text-sm underline underline-offset-4"
      >
        Manage profiles and network defaults
      </a>
    </div>
  );
}
type InventorySummary = NonNullable<
  Schema["ClientsResponse"]["policy_summaries"]
>[string];
function DeviceRow({
  row,
  select,
  summary,
}: {
  row: ClientRow;
  select: () => void;
  summary?: InventorySummary;
}) {
  const observation = row.observed[0];
  const name = row.configured?.name || observation?.name || row.configured?.id;
  const identity = {
    name,
    address: observation?.address || row.configured?.address || row.key,
    device: observation?.device,
    source: observation?.name_source,
    stale: observation?.name_fresh === false,
  };
  return (
    <div className="grid gap-3 p-4 sm:grid-cols-[1fr_1fr_auto] sm:items-center">
      <div className="min-w-0 space-y-1">
        <button
          aria-label={name || identity.address}
          className="min-h-10 text-left text-sm font-medium underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-ring"
          onClick={select}
        >
          <ClientIdentity {...identity} compact />
        </button>
        <p className="break-all text-xs text-muted-foreground">
          {row.configured ? row.key : "Unmatched · network defaults"}
        </p>
        {row.observed.map((o) => (
          <p
            key={o.address}
            className="break-all text-xs text-muted-foreground"
          >
            {o.address} ·{" "}
            {
              {
                address: "Exact address",
                dhcp_mac: "Authoritative DHCP MAC",
                cidr: "Network range",
                network: "Network defaults",
              }[o.matching_method ?? "network"]
            }
            {o.count !== undefined && (
              <span className="block">
                {count(o.count)} queries · {count(o.blocked)} blocked
              </span>
            )}
          </p>
        ))}
        {!row.observed.length && (
          <p className="text-xs text-muted-foreground">
            Configured · not observed in this window
          </p>
        )}
      </div>
      {row.configured ? (
        <DeviceSummary summary={summary} />
      ) : (
        <p className="text-xs text-muted-foreground">No configured identity</p>
      )}
      <div className="flex flex-wrap gap-2">
        <ClientDeviceButton {...identity} />
        {observation && (
          <Button
            variant="ghost"
            aria-label={`View queries for ${name || observation.address}`}
            onClick={() =>
              window.location.assign(
                `/queries?client=${encodeURIComponent(observation.address)}`,
              )
            }
          >
            Queries
          </Button>
        )}
        <Button variant="outline" onClick={select}>
          {row.configured ? "Edit policy" : "Configure device"}
        </Button>
      </div>
    </div>
  );
}
function DeviceSummary({ summary }: { summary?: InventorySummary }) {
  if (!summary)
    return (
      <p className="text-xs text-muted-foreground">
        Policy summary unavailable
      </p>
    );
  const effective = summary.active;
  return (
    <div className="space-y-1 text-xs text-muted-foreground">
      <p>
        {!effective
          ? "Policy not active"
          : effective.filtering
            ? "Filtering enabled"
            : effective.global_paused
              ? "Globally paused"
              : "Filtering off or paused"}
      </p>
      <p>
        Profile: {summary.desired.profile_id || "None"} ·{" "}
        {summary.desired.override_count} overrides
      </p>
      {effective?.source_unavailable && (
        <p className="text-destructive">Assigned source not active</p>
      )}
    </div>
  );
}
export function Profiles() {
  const profiles = useResource<{
    items: Schema["PolicyProfile"][];
    status: Schema["Activation"];
  }>("profiles");
  const clients = useResource<Schema["ClientsResponse"]>("clients");
  const [selected, setSelected] = useState<string>();
  const [creating, setCreating] = useState(false);
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <label className="text-sm">
          Policy
          <select
            aria-label="Policy to edit"
            className={`${selectClass} ml-2`}
            value={selected ?? ""}
            onChange={(e) => setSelected(e.target.value || undefined)}
          >
            <option value="">Network defaults</option>
            {profiles.data?.items.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name || p.id}
              </option>
            ))}
          </select>
        </label>
        <Button variant="outline" onClick={() => setCreating(true)}>
          Create profile
        </Button>
      </div>
      {profiles.error && <ErrorNotice error={profiles.error} />}
      {creating && (
        <CreateOwner
          scope="profile"
          revision={profiles.data?.status.saved_revision ?? ""}
          cancel={() => setCreating(false)}
          saved={(id) => {
            setCreating(false);
            setSelected(id);
            void profiles.reload();
          }}
        />
      )}
      {selected && (
        <p className="text-xs text-muted-foreground">
          Assigned devices:{" "}
          {clients.data?.items
            ?.filter((c) => c.profile === selected)
            .map((c) => c.name || c.policy_id)
            .join(", ") || "None"}
        </p>
      )}
      <PolicyEditor
        scope={selected ? "profile" : "network"}
        id={selected}
        onDeleted={
          selected
            ? () => {
                setSelected(undefined);
                void profiles.reload();
              }
            : undefined
        }
      />
    </div>
  );
}
function CreateOwner({
  scope,
  observed,
  revision,
  cancel,
  saved,
}: {
  scope: PolicyScope;
  observed?: Schema["ObservedClients"]["items"][number];
  revision: string;
  cancel: () => void;
  saved: (id: string) => void;
}) {
  const [id, setID] = useState("");
  const [editRevision, setEditRevision] = useState(revision);
  useEffect(() => {
    if (!editRevision && revision) setEditRevision(revision);
  }, [revision, editRevision]);
  const [name, setName] = useState(observed?.name ?? "");
  const [address, setAddress] = useState(observed?.address ?? "");
  const [useMAC, setUseMAC] = useState(!!observed?.authoritative_mac);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  const [status, setStatus] = useState<Schema["Activation"]>();
  return (
    <form
      className={panelClass}
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError(undefined);
        try {
          const result = await api.send<Schema["Activation"]>(
            "client-policy",
            "PATCH",
            {
              revision: editRevision,
              scope,
              id: id.trim(),
              create: true,
              ...(name.trim() ? { name: name.trim() } : {}),
              ...(scope === "client"
                ? useMAC
                  ? { lease_address: observed!.address }
                  : { selectors: { addresses: words(address) } }
                : {}),
            },
          );
          setStatus(result);
          saved(id.trim());
        } catch (e) {
          setError(e as Error);
        } finally {
          setBusy(false);
        }
      }}
    >
      <h3 className="text-sm font-medium">
        New {scope === "client" ? "device" : "profile"}
      </h3>
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="text-sm">
          Stable ID
          <Input
            required
            value={id}
            onChange={(e) => setID(e.target.value)}
            placeholder="tablet"
          />
        </label>
        <label className="text-sm">
          Name
          <Input value={name} onChange={(e) => setName(e.target.value)} />
        </label>
      </div>
      {scope === "client" && (
        <>
          {observed?.authoritative_mac && (
            <label className="flex min-h-10 items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={useMAC}
                onChange={(e) => setUseMAC(e.target.checked)}
              />
              Use authoritative lease MAC: {observed.authoritative_mac}
            </label>
          )}
          {!useMAC && (
            <label className="block text-sm">
              Matching addresses
              <textarea
                required
                className={`${selectClass} mt-1 w-full`}
                value={address}
                onChange={(e) => setAddress(e.target.value)}
              />
            </label>
          )}
          <p className="text-xs text-muted-foreground">
            {useMAC
              ? "Saves the lease MAC only, without a dynamic address selector."
              : "Exact addresses bind policy to these addresses until you relink them."}
          </p>
        </>
      )}
      {error && <ErrorNotice error={error} />}
      {error instanceof APIError && error.status === 409 && (
        <Button
          type="button"
          variant="outline"
          onClick={async () => {
            try {
              const current = await api.get<{ status: Schema["Activation"] }>(
                scope === "client" ? "clients" : "profiles",
              );
              setEditRevision(current.status.saved_revision);
              setError(undefined);
            } catch (e) {
              setError(e as Error);
            }
          }}
        >
          Reload revision
        </Button>
      )}
      {status && <ActivationStatus status={status} />}
      <div className="flex gap-2">
        <Button disabled={busy || !editRevision} type="submit">
          {busy
            ? "Creating…"
            : `Create ${scope === "client" ? "device" : "profile"}`}
        </Button>
        <Button type="button" variant="ghost" onClick={cancel}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
