import { useEffect, useRef, useState } from "react";
import { api, count, APIError } from "@/lib/api";
import { useResource } from "@/lib/hooks";
import { ErrorNotice, Resource } from "@/components/data";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { DeviceIconField } from "@/components/device-icon";
import { Dialog, DialogContent, DialogTitle, DialogDescription } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHeader, TableRow } from "@/components/ui/table";
import { ActivationStatus, PolicyEditor } from "./policy";
import {
  mergeClients,
  clientIdentity,
  clientLastSeen,
  clientCount,
  panelClass,
  selectClass,
  words,
  policyID,
  type ClientRow,
  type Schema,
  type PolicyScope,
} from "./model";
import { DomainInspector } from "./inspect";
import { usePolicyDraft } from "./draft";
import { ProfileAssignment, ProfileMap } from "./assignment";
import { ProfileEditorDialog } from "./profile-editor-dialog";
import { ClientIdentity, ClientDeviceButton } from "@/components/client-identity";
import { SortableHead, useSortableTable, type SortColumn } from "@/components/table-sorting";

export default function Clients({
  range,
  selected,
  onSelect,
}: {
  range: string;
  selected?: string;
  onSelect: (id?: string) => void;
}) {
  const draft = usePolicyDraft();
  const settingsTrigger = useRef<HTMLElement | null>(null);
  const state = useResource<Schema["ClientsResponse"]>(`clients?${range}&limit=200`);
  const [search, setSearch] = useState("");
  const [assignmentStatus, setAssignmentStatus] = useState<Schema["Activation"]>();
  const [creating, setCreating] = useState<ClientRow>();
  const profiles = useResource<{ items: Schema["PolicyProfile"][] }>("profiles");
  const rows = mergeClients(state.data ?? {}).filter((row) =>
    JSON.stringify(row).toLowerCase().includes(search.toLowerCase()),
  );
  const columns: (SortColumn<ClientRow> & { width: string; align?: "right" })[] = [
    {
      key: "device",
      label: "Device",
      sortType: "address",
      width: "28%",
      sortValue: (r) => {
        const identity = clientIdentity(r);
        return identity.name || identity.address;
      },
    },
    {
      key: "profile",
      label: "Profile",
      width: "18%",
      sortValue: (r) => {
        const id = r.configured?.profile;
        const profile = profiles.data?.items.find((p) => p.id === id);
        return id ? profile?.name || id : "Network defaults";
      },
    },
    {
      key: "last_seen",
      label: "Last seen",
      width: "17%",
      sortType: "datetime",
      sortValue: clientLastSeen,
    },
    {
      key: "count",
      label: "Queries",
      width: "9%",
      align: "right",
      sortType: "number",
      sortValue: (r) => clientCount(r, "count"),
    },
    {
      key: "blocked",
      label: "Blocked",
      width: "9%",
      align: "right",
      sortType: "number",
      sortValue: (r) => clientCount(r, "blocked"),
    },
    { key: "actions", label: "Actions", width: "19%", align: "right", sortable: false },
  ];
  const table = useSortableTable({
    items: rows,
    columns,
    initialSorting: [{ id: "count", desc: true }],
    getRowId: (r) => r.key,
  });
  if (selected)
    return (
      <div className="space-y-4">
        <Button variant="ghost" onClick={() => onSelect()}>
          Back to devices
        </Button>
        <PolicyEditor
          scope="client"
          id={selected}
          onDirty={draft.onDirty}
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
          placeholder="Search name or address"
          className="max-w-sm"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </div>
      {creating && (
        <CreateOwner
          key={creating.key}
          onDirty={draft.onCreateDirty}
          scope="client"
          observed={creating.observed[0]}
          revision={state.data?.status.saved_revision ?? ""}
          cancel={() => {
            if (!draft.confirmLeave()) return;
            setCreating(undefined);
            requestAnimationFrame(() => settingsTrigger.current?.focus());
          }}
          saved={(id) => {
            setCreating(undefined);
            void state.reload();
            onSelect(id);
          }}
        />
      )}
      <Resource state={state} retry={() => void state.reload()}>
        {assignmentStatus && (
          <ActivationStatus
            status={
              state.data?.status.saved_revision === assignmentStatus.saved_revision
                ? state.data.status
                : assignmentStatus
            }
          />
        )}
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
            Showing the first 200 observed addresses. Sorting applies to loaded devices. Narrow the
            history window to see others.
          </p>
        )}
        <div className="overflow-hidden rounded-lg border border-border bg-background">
          <Table className="min-w-240 table-fixed">
            <TableHeader>
              <TableRow>
                {table.getHeaderGroups()[0].headers.map((header, i) => (
                  <SortableHead
                    key={header.id}
                    column={header.column}
                    sorted={header.column.getIsSorted()}
                    label={columns[i].label}
                    align={columns[i].align}
                    className={`bg-muted ${i === 0 || i === 5 ? "px-4" : ""}`}
                    style={{ width: columns[i].width, textAlign: columns[i].align }}
                  />
                ))}
              </TableRow>
            </TableHeader>
            <TableBody>
              {table.getRowModel().rows.map(({ original: row }) => (
                <DeviceRow
                  key={row.key}
                  row={row}
                  summary={state.data?.policy_summaries?.[row.key]}
                  profiles={profiles.data?.items ?? []}
                  reload={() => state.reload()}
                  onStatus={setAssignmentStatus}
                  select={() => {
                    if (row.configured) onSelect(row.key);
                    else if (draft.confirmLeave()) {
                      settingsTrigger.current = document.activeElement as HTMLElement;
                      setCreating(row);
                    }
                  }}
                />
              ))}
              {!rows.length && (
                <TableRow>
                  <TableCell colSpan={6} className="p-5 text-center text-sm text-muted-foreground">
                    {search
                      ? "No devices match this search."
                      : "Devices will appear here when they make DNS requests."}
                    {search && (
                      <Button variant="link" onClick={() => setSearch("")}>
                        Clear search
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
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
type InventorySummary = NonNullable<Schema["ClientsResponse"]["policy_summaries"]>[string];
function DeviceRow({
  row,
  select,
  summary,
  profiles,
  reload,
  onStatus,
}: {
  row: ClientRow;
  select: () => void;
  summary?: InventorySummary;
  profiles: Schema["PolicyProfile"][];
  reload: () => Promise<unknown>;
  onStatus: (status: Schema["Activation"]) => void;
}) {
  const identity = clientIdentity(row);
  const { name } = identity;
  const lastSeen = clientLastSeen(row);
  return (
    <TableRow>
      <TableCell className="px-4 py-2.5">
        {identity.address ? (
          <button
            aria-label={`View queries for ${name || identity.address}`}
            className="min-h-10 max-w-full text-left text-sm font-medium underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-ring"
            onClick={() =>
              window.location.assign(`/queries?client=${encodeURIComponent(identity.address)}`)
            }
          >
            <ClientIdentity {...identity} compact />
          </button>
        ) : (
          <ClientIdentity {...identity} compact />
        )}
      </TableCell>
      <TableCell className="py-2.5 text-xs">
        <ProfileAssignment row={row} profiles={profiles} reload={reload} onStatus={onStatus} />
        {!!summary?.desired.override_count && (
          <p className="mt-1 text-muted-foreground">Custom settings</p>
        )}
        {summary?.active?.source_unavailable && (
          <p className="text-destructive">Filter list unavailable</p>
        )}
        {summary?.active && !summary.active.filtering && (
          <p className="text-muted-foreground">Filtering off or paused</p>
        )}
      </TableCell>
      <TableCell className="py-2.5 text-xs text-muted-foreground">
        {lastSeen ? new Date(lastSeen).toLocaleString() : "Not seen in this period"}
      </TableCell>
      <TableCell className="py-2.5 text-right tabular-nums">
        {count(clientCount(row, "count"))}
      </TableCell>
      <TableCell className="py-2.5 text-right tabular-nums">
        {count(clientCount(row, "blocked"))}
      </TableCell>
      <TableCell className="px-4 py-2.5">
        <div className="flex justify-end gap-1">
          <ClientDeviceButton {...identity} />
          <Button size="sm" variant="outline" onClick={select}>
            Settings
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
}
export function Profiles() {
  const draft = usePolicyDraft();
  const createTrigger = useRef<HTMLButtonElement>(null);
  const editTrigger = useRef<HTMLElement | null>(null);
  const profiles = useResource<{
    items: Schema["PolicyProfile"][];
    status: Schema["Activation"];
  }>("profiles");
  const clients = useResource<Schema["ClientsResponse"]>("clients");
  const [selected, setSelected] = useState<string>();
  const [tab, setTab] = useState<"network" | "profiles">("network");
  const [creating, setCreating] = useState(false);
  const [createBusy, setCreateBusy] = useState(false);
  const closeCreate = () => {
    if (!createBusy && draft.confirmLeave()) setCreating(false);
  };
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border pb-3">
        <div className="flex gap-1" aria-label="Settings scope">
          {(["network", "profiles"] as const).map((value) => (
            <Button
              key={value}
              variant="ghost"
              className={
                tab === value
                  ? "rounded-none border-b-2 border-primary font-medium text-primary"
                  : "rounded-none border-b-2 border-transparent"
              }
              aria-pressed={tab === value}
              onClick={() => {
                if (tab === value) return;
                if (!draft.confirmLeave()) return;
                draft.onDirty(false);
                draft.onCreateDirty(false);
                setCreating(false);
                setTab(value);
              }}
            >
              {value === "network" ? "Network defaults" : "Profiles"}
            </Button>
          ))}
        </div>
        <Button
          ref={createTrigger}
          onClick={() => {
            if (draft.confirmLeave()) {
              draft.onDirty(false);
              setTab("profiles");
              setCreating(true);
            }
          }}
          disabled={creating}
        >
          Create profile
        </Button>
      </div>
      {tab === "profiles" && (
        <>
          <Resource state={clients} retry={() => void clients.reload()}>
            <ProfileMap
              rows={mergeClients(clients.data ?? {})}
              liveStatus={clients.data?.status}
              profiles={profiles.data?.items ?? []}
              reload={() => clients.reload()}
              disabled={creating || draft.dirty}
              onEdit={(id, trigger) => {
                if (draft.confirmLeave()) {
                  draft.onDirty(false);
                  editTrigger.current = trigger;
                  setSelected(id);
                }
              }}
            />
          </Resource>
        </>
      )}
      {profiles.error && <ErrorNotice error={profiles.error} />}
      <Dialog
        open={creating}
        onOpenChange={(open) => {
          if (!open) closeCreate();
        }}
      >
        <DialogContent
          onCloseAutoFocus={(e) => {
            e.preventDefault();
            if (!selected) createTrigger.current?.focus({ preventScroll: true });
          }}
        >
          <DialogTitle>Create profile</DialogTitle>
          <DialogDescription>
            Give this profile a name. You can assign devices and customize its settings next.
          </DialogDescription>
          <CreateOwner
            scope="profile"
            onDirty={draft.onCreateDirty}
            onBusy={setCreateBusy}
            revision={profiles.data?.status.saved_revision ?? ""}
            cancel={closeCreate}
            saved={(id) => {
              draft.onDirty(false);
              setCreating(false);
              editTrigger.current = createTrigger.current;
              setSelected(id);
              void profiles.reload();
            }}
          />
        </DialogContent>
      </Dialog>
      {tab === "network" && <PolicyEditor scope="network" onDirty={draft.onDirty} />}
      {tab === "profiles" && selected && (
        <ProfileEditorDialog
          key={selected}
          id={selected}
          name={profiles.data?.items.find((p) => p.id === selected)?.name || selected}
          onDirty={draft.onDirty}
          confirmLeave={draft.confirmLeave}
          returnFocus={editTrigger}
          onClose={() => {
            setSelected(undefined);
            void profiles.reload();
          }}
          onDeleted={() => {
            editTrigger.current = createTrigger.current;
            setSelected(undefined);
            void profiles.reload();
          }}
        />
      )}
    </div>
  );
}
function CreateOwner({
  scope,
  observed,
  revision,
  cancel,
  saved,
  onDirty,
  onBusy,
}: {
  scope: PolicyScope;
  observed?: Schema["ObservedClients"]["items"][number];
  revision: string;
  cancel: () => void;
  saved: (id: string) => void;
  onDirty: (dirty: boolean) => void;
  onBusy?: (busy: boolean) => void;
}) {
  const [initialID] = useState(() => policyID(scope === "client" ? "device" : "profile"));
  const [id, setID] = useState(initialID);
  const [editRevision, setEditRevision] = useState(revision);
  useEffect(() => {
    if (!editRevision && revision) setEditRevision(revision);
  }, [revision, editRevision]);
  const [name, setName] = useState(observed?.name ?? "");
  const [icon, setIcon] = useState("");
  const [address, setAddress] = useState(observed?.address ?? "");
  const [useMAC, setUseMAC] = useState(!!observed?.authoritative_mac);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    onBusy?.(busy);
  }, [busy, onBusy]);
  useEffect(() => () => onBusy?.(false), [onBusy]);
  const [error, setError] = useState<Error>();
  const [status, setStatus] = useState<Schema["Activation"]>();
  useEffect(() => {
    onDirty(
      !status &&
        (id !== initialID ||
          name !== (observed?.name ?? "") ||
          icon !== "" ||
          address !== (observed?.address ?? "") ||
          useMAC !== !!observed?.authoritative_mac),
    );
  }, [id, initialID, name, icon, address, useMAC, observed, status, onDirty]);
  useEffect(() => () => onDirty(false), [onDirty]);
  const firstField = useRef<HTMLInputElement>(null);
  useEffect(() => {
    firstField.current?.focus();
  }, []);
  return (
    <form
      className={scope === "profile" ? "space-y-4" : panelClass}
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError(undefined);
        try {
          const result = await api.send<Schema["Activation"]>("client-policy", "PATCH", {
            revision: editRevision,
            scope,
            id: id.trim(),
            create: true,
            ...(name.trim() ? { name: name.trim() } : {}),
            ...(scope === "client" && icon ? { icon } : {}),
            ...(scope === "client"
              ? useMAC
                ? { lease_address: observed!.address }
                : { selectors: { addresses: words(address) } }
              : {}),
          });
          setStatus(result);
          onDirty(false);
          saved(id.trim());
        } catch (e) {
          setError(e as Error);
        } finally {
          setBusy(false);
        }
      }}
    >
      {scope === "client" && (
        <h3 className="text-sm font-medium">Settings for {observed?.name || observed?.address}</h3>
      )}
      <div className="grid gap-3">
        <label className="text-sm">
          Name
          <Input ref={firstField} required value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        {scope === "client" && <DeviceIconField value={icon} onChange={setIcon} />}
      </div>
      <details>
        <summary className="text-xs text-muted-foreground">Advanced identification</summary>
        <label className="text-sm">
          Stable ID
          <Input required value={id} onChange={(e) => setID(e.target.value)} />
        </label>
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
      </details>
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
          {busy ? "Creating…" : scope === "client" ? "Save device settings" : "Create profile"}
        </Button>
        <Button type="button" variant="ghost" disabled={busy} onClick={cancel}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
