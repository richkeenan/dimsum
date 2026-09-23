import { useState } from "react";
import {
  ListSubscriptions,
  RefreshListsButton,
  type ListToggle,
} from "./lists";
import {
  ClientDeviceButton,
  ClientIdentity,
} from "@/components/client-identity";
import type { Device } from "@/lib/api";
import {
  api,
  rows,
  count,
  text,
  collectionRows,
  normalizeSettings,
  type Row,
  type Settings,
  type Mutation,
} from "@/lib/api";
import { useResource } from "@/lib/hooks";
import { DataTable, Details, ErrorNotice, Resource } from "@/components/data";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Revision } from "./settings";
import {
  UpstreamEditor,
  UpstreamName,
  UpstreamConnectionTest,
  UpstreamPoolSummary,
} from "./upstreams";
type Field = {
  key: string;
  label: string;
  type?: "number" | "boolean";
  options?: string[];
  optional?: boolean;
  help?: string;
  placeholder?: string;
};
const fields: Record<string, Field[]> = {
  lists: [
    {
      key: "url",
      label: "List URL",
      placeholder: "https://example.com/blocklist.txt",
      help: "A direct HTTPS link to a downloadable domain blocklist.",
    },
    {
      key: "dialect",
      label: "Format",
      options: ["domains", "hosts", "dns-adblock"],
    },
    { key: "domain_kind", label: "Domain scope", options: ["exact", "suffix"] },
    { key: "enabled", label: "Enabled", type: "boolean" },
    {
      key: "default_apply",
      label: "Apply by default on the network",
      type: "boolean",
    },
  ],
  rules: [
    {
      key: "kind",
      label: "Match type",
      options: ["exact", "suffix", "wildcard", "regex"],
    },
    { key: "action", label: "Action", options: ["deny", "allow"] },
    {
      key: "pattern",
      label: "Domain or pattern",
      placeholder: "ads.example.com",
    },
    { key: "enabled", label: "Enabled", type: "boolean" },
  ],
  records: [
    { key: "name", label: "DNS name" },
    {
      key: "type",
      label: "Record type",
      options: ["A", "AAAA", "CNAME", "PTR"],
    },
    {
      key: "value",
      label: "Address or target",
      help: "A: IPv4 address. AAAA: IPv6 address. CNAME and PTR: target hostname.",
    },
    { key: "ttl", label: "Cache lifetime (seconds)", type: "number" },
    {
      key: "auto_ptr",
      label: "Create a reverse lookup too",
      type: "boolean",
      help: "For A and AAAA records, also resolve the IP address back to this name.",
    },
  ],
  upstreams: [{ key: "address", label: "Address and port" }],
  clients: [
    { key: "address", label: "Observed address" },
    { key: "name", label: "Friendly name" },
  ],
};
const columns: Record<string, string[]> = {
  rules: ["pattern", "action", "kind", "enabled"],
  records: ["name", "type", "value", "ttl"],
  upstreams: ["address"],
  clients: ["name", "address"],
};
const optionLabels: Record<string, string> = {
  deny: "Block",
  allow: "Allow",
  exact: "Exact domain only",
  suffix: "Domain and subdomains",
  wildcard: "Wildcard pattern",
  regex: "Regular expression",
  domains: "Domain names",
  hosts: "Hosts file",
  "dns-adblock": "DNS adblock",
};
const listFormatHelp: Record<string, string> = {
  domains: "One domain name per line.",
  hosts: "IP address and domain name pairs, one per line.",
  "dns-adblock":
    "Domain blocking rules such as ||example.com^. Browser filter rules are not supported.",
};
export function editorDefaults(kind: string): Row {
  const defaults = Object.fromEntries(
    fields[kind].map((f) => [
      f.key,
      f.type === "boolean" ? true : (f.options?.[0] ?? ""),
    ]),
  );
  if (kind === "lists" || kind === "rules")
    defaults.id = `${kind.slice(0, -1)}-${Array.from(crypto.getRandomValues(new Uint8Array(12)), (byte) => byte.toString(16).padStart(2, "0")).join("")}`;
  if (kind === "records")
    Object.assign(defaults, { ttl: 300, auto_ptr: false });
  if (kind === "lists") defaults.default_apply = false;
  return defaults;
}
export default function Configuration({
  kind,
  range,
  onClientQueries,
}: {
  kind: string;
  range: string;
  onClientQueries?: (address: string) => void;
}) {
  const [tick, setTick] = useState(0);
  const state = useResource<Row>(
    kind + (kind === "clients" ? "?" + range + "&limit=200" : ""),
    tick,
  );
  const settings = useResource<Settings>("settings", tick);
  const [editing, setEditing] = useState<Row>();
  const [original, setOriginal] = useState<Row>();
  const [editRevision, setEditRevision] = useState("");
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  const [listToggle, setListToggle] = useState<ListToggle>();
  const [notice, setNotice] = useState<Row>();
  const [dashboardHost, setDashboardHost] = useState<{
    name: string;
    url: string;
    revision: string;
  }>();
  const [dashboardURL, setDashboardURL] = useState("");
  const [hostError, setHostError] = useState<Error>();
  const revision = normalizeSettings(state.data).revision;
  const ready =
    !!revision &&
    !state.loading &&
    !state.error &&
    !busy &&
    !(kind === "lists" && state.isFetching);
  const configuredClients = collectionRows(state.data);
  const observedClients = rows(state.data?.observed);
  const devices = [
    ...observedClients.map((observed) => {
      const configured = configuredClients.find(
        (client) => client.address === observed.address,
      );
      return {
        ...observed,
        ...configured,
        ...(configured ? { name_source: "override", name_fresh: true } : {}),
      };
    }),
    ...configuredClients.filter(
      (client) =>
        !observedClients.some(
          (observed) => observed.address === client.address,
        ),
    ),
  ];
  function open(row?: Row) {
    if (!ready) return;
    if (kind === "lists" && row && row.default_apply === undefined)
      row = { ...row, default_apply: row.enabled === true };
    const defaults = editorDefaults(kind);
    setOriginal(row?.__index === undefined ? undefined : row);
    setEditRevision(revision);
    setEditing(
      row
        ? {
            ...defaults,
            ...(row.id ? { id: row.id } : {}),
            ...Object.fromEntries(
              fields[kind].map((f) => [f.key, row[f.key] ?? defaults[f.key]]),
            ),
          }
        : defaults,
    );
    setError(undefined);
  }
  async function mutate(method: string, row?: Row) {
    setBusy(true);
    setError(undefined);
    try {
      if (!editRevision)
        throw new Error(
          "The configuration revision is unavailable. Reload and retry.",
        );
      const body: Mutation = { revision: editRevision };
      if (method === "POST")
        body.item = kind === "upstreams" ? row?.address : row;
      else if (method === "DELETE") body.index = Number(original?.__index);
      else
        body.edits = Object.entries(row ?? {})
          .filter(([key, value]) => value !== original?.[key])
          .map(([key, value]) => ({
            path:
              kind === "upstreams"
                ? [String(original?.__index)]
                : [String(original?.__index), key],
            value: value as string | number | boolean,
          }));
      if (method === "PATCH" && !body.edits?.length) {
        if (kind === "records") {
          // Saving an existing record can also offer its dashboard hostname.
          body.edits = [
            {
              path: [String(original?.__index), "name"],
              value: String(row?.name),
            },
          ];
        } else {
          setEditing(undefined);
          return;
        }
      }
      const result = await api.send<Row>(kind, method, body);
      setNotice(result);
      setDashboardURL("");
      if (kind === "records" && method !== "DELETE") {
        const name = new URL(`http://${row?.name}`).hostname
          .toLowerCase()
          .replace(/\.$/, "");
        const candidate = rows(result.dashboard_hosts).find(
          (host) => host.name === name,
        );
        if (candidate && result.saved_revision) {
          setHostError(undefined);
          setDashboardHost({
            name,
            url: String(candidate.url),
            revision: String(result.saved_revision),
          });
        }
      }
      setEditing(undefined);
      setTick((t) => t + 1);
    } catch (e) {
      setError(e as Error);
    } finally {
      if (kind === "lists") await state.reload();
      setBusy(false);
    }
  }
  async function acceptDashboardHost() {
    if (!dashboardHost) return;
    setBusy(true);
    setHostError(undefined);
    try {
      const result = await api.send<Row>("records", "PATCH", {
        revision: dashboardHost.revision,
        accept_admin_host: dashboardHost.name,
      });
      setNotice(result);
      setDashboardURL(dashboardHost.url);
      setDashboardHost(undefined);
      setTick((t) => t + 1);
    } catch (e) {
      setHostError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  async function toggleList(row: Row, enabled: boolean) {
    if (!ready) return;
    setError(undefined);
    setBusy(true);
    setListToggle({ id: row.id, enabled });
    try {
      await api.send<Row>(
        "lists",
        row.__index === undefined ? "POST" : "PATCH",
        {
          revision,
          ...(row.__index === undefined
            ? {
                item: {
                  ...editorDefaults("lists"),
                  default_apply: false,
                  url: row.url,
                  dialect: row.dialect,
                  domain_kind: row.domain_kind,
                  enabled,
                },
              }
            : {
                edits: [
                  { path: [String(row.__index), "enabled"], value: enabled },
                ],
              }),
        },
      );
    } catch (e) {
      setError(e as Error);
    } finally {
      // Join the refetch triggered by configuration-changed, or start one on failure.
      await state.reload({ cancelRefetch: false });
      setListToggle(undefined);
      setBusy(false);
    }
  }
  return (
    <div className="min-w-0 [&_p]:leading-relaxed">
      <Resource state={settings}>
        <Revision value={settings.data} />
      </Resource>
      {kind === "upstreams" && (
        <UpstreamPoolSummary config={settings.data?.config} />
      )}
      {kind === "clients" && (
        <Resource state={state}>
          <section className="mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background">
            <div className="flex flex-wrap items-center justify-between gap-2.5 border-b border-border px-[18px] py-[13px]">
              <h2 className="text-sm font-medium">Devices</h2>
              <Button disabled={!ready} onClick={() => open()}>
                Name an address
              </Button>
            </div>
            {state.data?.observed_available === true || devices.length > 0 ? (
              <>
                {(state.data?.observed as Row)?.truncated === true && (
                  <p className="my-3 rounded-[5px] border border-border border-l-[3px] border-l-[#b69860] bg-muted px-3.5 py-3 text-xs [overflow-wrap:anywhere]">
                    The observed client list is truncated. Narrow the time range
                    to inspect more identities.
                  </p>
                )}
                <DataTable
                  items={devices}
                  columns={[
                    {
                      key: "name",
                      label: "Client",
                      render: (r) => (
                        <button
                          className="max-w-full text-left hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                          aria-label={`View queries for ${r.name || r.address}`}
                          onClick={() => onClientQueries?.(text(r.address))}
                        >
                          <ClientIdentity
                            address={text(r.address)}
                            name={r.name ? String(r.name) : undefined}
                            device={r.device as Device | undefined}
                            stale={r.name_fresh === false}
                            source={
                              r.name_source ? String(r.name_source) : undefined
                            }
                          />
                        </button>
                      ),
                    },
                    {
                      key: "last_seen",
                      label: "Last seen",
                      render: (r) =>
                        r.last_seen
                          ? new Date(String(r.last_seen)).toLocaleString()
                          : "—",
                    },
                    {
                      key: "count",
                      label: "Queries",
                      render: (r) => count(r.count),
                    },
                    {
                      key: "blocked",
                      label: "Blocked",
                      render: (r) => count(r.blocked),
                    },
                    {
                      key: "actions",
                      label: "Actions",
                      render: (r) => (
                        <div className="flex items-center gap-2">
                          <ClientDeviceButton
                            address={text(r.address)}
                            name={r.name ? String(r.name) : undefined}
                            device={r.device as Device | undefined}
                            source={
                              r.name_source ? String(r.name_source) : undefined
                            }
                          />
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={!ready}
                            onClick={() =>
                              open(
                                collectionRows(state.data).find(
                                  (c) => c.address === r.address,
                                ) ?? r,
                              )
                            }
                          >
                            Set name
                          </Button>
                        </div>
                      ),
                    },
                  ]}
                />
              </>
            ) : (
              <p className="p-9 text-center text-muted-foreground">
                Observed client history is unavailable.
              </p>
            )}
          </section>
        </Resource>
      )}
      {error && !editing && (
        <ErrorNotice error={error} retry={() => setTick((t) => t + 1)} />
      )}
      {kind !== "clients" && (
        <section className="mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background">
          <div className="flex flex-wrap items-center justify-between gap-2.5 border-b border-border px-[18px] py-[13px]">
            <h2 className="text-sm font-medium">
              {kind === "lists"
                ? "Blocklist subscriptions"
                : kind === "clients"
                  ? "Configured friendly names"
                  : "Configured " + kind}
            </h2>
            <div className="flex flex-wrap items-center gap-2">
              {kind === "lists" && (
                <RefreshListsButton
                  disabled={!ready}
                  completed={() => setTick((t) => t + 1)}
                />
              )}
              <Button disabled={!ready} onClick={() => open()}>
                {kind === "lists"
                  ? "Add custom URL"
                  : kind === "clients"
                    ? "Name an address"
                    : "Add " +
                      (kind === "upstreams" ? "upstream" : kind.slice(0, -1))}
              </Button>
            </div>
          </div>
          <Resource state={state} retry={() => setTick((t) => t + 1)}>
            {kind === "lists" ? (
              <ListSubscriptions
                data={state.data}
                disabled={!ready}
                pending={listToggle}
                toggle={(row, enabled) => void toggleList(row, enabled)}
                edit={open}
              />
            ) : (
              <DataTable
                items={collectionRows(state.data)}
                columns={[
                  ...columns[kind].map((key) => ({
                    key,
                    label:
                      (
                        {
                          enabled: "Status",
                          pattern: "Domain or pattern",
                          kind: "Matches",
                          action: "Action",
                          ttl: "Lifetime (s)",
                          name: "Name",
                          address: "Address",
                          type: "Type",
                          value: "Target",
                        } as Record<string, string>
                      )[key] ?? key,
                    render: (r: Row) =>
                      kind === "upstreams" && key === "address" ? (
                        <UpstreamName address={text(r.address)} />
                      ) : key === "enabled" ? (
                        r.enabled === true ? (
                          "Enabled"
                        ) : r.enabled === false ? (
                          "Disabled"
                        ) : (
                          "Unknown"
                        )
                      ) : key === "ttl" ? (
                        count(r[key])
                      ) : (
                        (optionLabels[String(r[key])] ?? text(r[key]))
                      ),
                  })),
                  {
                    key: "actions",
                    label: "Actions",
                    align: "right",
                    render: (r) => (
                      <div className="flex items-center justify-end gap-2 whitespace-nowrap">
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={!ready}
                          onClick={() => open(r)}
                        >
                          Edit
                        </Button>
                        {kind === "upstreams" && (
                          <UpstreamConnectionTest
                            key={text(r.address)}
                            address={text(r.address)}
                          />
                        )}
                      </div>
                    ),
                  },
                ]}
                empty={"No " + kind + " returned by the service."}
              />
            )}
            {kind === "lists" &&
              rows(state.data).some((r) => r.homepage || r.license) && (
                <div className="p-5 [&>p]:mb-[18px] [&>p]:text-xs [&>p]:text-muted-foreground">
                  {rows(state.data).map((r) => (
                    <p key={text(r.id)}>
                      {text(r.id)} · {text(r.license)} · {text(r.homepage)}
                    </p>
                  ))}
                </div>
              )}
          </Resource>
        </section>
      )}
      {notice && (
        <section
          className="mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background p-5 [&>p]:mb-[18px] [&>p]:text-xs [&>p]:text-muted-foreground"
          role="status"
        >
          <p>
            {notice.kind
              ? "Operation started. View progress in Backups & jobs."
              : "Changes saved."}
          </p>
          {dashboardURL && (
            <p>
              Dashboard address added. No restart needed. Open{" "}
              <a className="text-primary underline" href={dashboardURL}>
                {dashboardURL}
              </a>
            </p>
          )}
        </section>
      )}
      {kind === "rules" && <RuleTester />}
      <Dialog
        open={!!dashboardHost}
        onOpenChange={(open) => {
          if (!open && !busy) setDashboardHost(undefined);
        }}
      >
        <DialogContent>
          <DialogTitle>
            Use {dashboardHost?.name} for the dashboard too?
          </DialogTitle>
          <DialogDescription>
            This address belongs to this dimsum server. Add it to accepted hosts
            to open the dashboard at {dashboardHost?.url}.
          </DialogDescription>
          {hostError && <ErrorNotice error={hostError} />}
          <div className="flex flex-wrap justify-end gap-2">
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => setDashboardHost(undefined)}
            >
              Not now
            </Button>
            <Button disabled={busy} onClick={() => void acceptDashboardHost()}>
              {busy ? "Adding…" : "Add to accepted hosts"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
      {kind === "upstreams" && editing && (
        <UpstreamEditor
          original={original}
          revision={editRevision}
          configured={collectionRows(state.data)}
          close={() => setEditing(undefined)}
          saved={(result) => {
            setNotice(result);
            setEditing(undefined);
            setTick((t) => t + 1);
          }}
        />
      )}
      <Dialog
        open={!!editing && kind !== "upstreams"}
        onOpenChange={(open) => {
          if (!open && !busy) setEditing(undefined);
        }}
      >
        <DialogContent
          aria-describedby={undefined}
          className={`max-h-[calc(100dvh-32px)] w-[calc(100vw-32px)] min-w-0 overflow-y-auto [&>*]:min-w-0 ${kind === "lists" ? "sm:max-w-[720px] sm:p-8" : ""}`}
        >
          <DialogTitle>
            {original ? "Edit" : "Add"}{" "}
            {kind === "clients" ? "friendly name" : kind.slice(0, -1)}
          </DialogTitle>
          {editing && (
            <form
              className="min-w-0"
              onSubmit={(e) => {
                e.preventDefault();
                void mutate(original ? "PATCH" : "POST", editing);
              }}
            >
              <div
                className={`mt-3.5 mb-6 grid min-w-0 grid-cols-1 gap-x-6 gap-y-5 [&>*]:min-w-0 ${kind === "lists" ? "sm:grid-cols-2" : "min-[701px]:grid-cols-2"}`}
              >
                {fields[kind].map((f) => (
                  <label
                    className={`flex min-w-0 flex-col gap-1.5 text-xs font-normal ${kind === "lists" && f.key === "url" ? "sm:col-span-2" : ""}`}
                    key={f.key}
                  >
                    {f.label}
                    {f.type === "boolean" ? (
                      <select
                        className="min-h-9 w-full min-w-0 rounded-md border border-input bg-background px-2.5 py-2 text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                        aria-label={f.label}
                        value={String(editing[f.key])}
                        onChange={(e) =>
                          setEditing({
                            ...editing,
                            [f.key]: e.target.value === "true",
                          })
                        }
                      >
                        <option value="true">Enabled</option>
                        <option value="false">Disabled</option>
                      </select>
                    ) : f.options ? (
                      <select
                        className="min-h-9 w-full min-w-0 rounded-md border border-input bg-background px-2.5 py-2 text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                        aria-label={f.label}
                        value={text(editing[f.key])}
                        onChange={(e) =>
                          setEditing({ ...editing, [f.key]: e.target.value })
                        }
                      >
                        {f.options.map((o) => (
                          <option key={o} value={o}>
                            {optionLabels[o] ?? o}
                          </option>
                        ))}
                      </select>
                    ) : (
                      <Input
                        required={!f.optional}
                        placeholder={f.placeholder}
                        min={f.type === "number" ? 0 : undefined}
                        type={f.type === "number" ? "number" : "text"}
                        value={String(editing[f.key] ?? "")}
                        onChange={(e) =>
                          setEditing({
                            ...editing,
                            [f.key]:
                              f.type === "number"
                                ? Number(e.target.value)
                                : e.target.value,
                          })
                        }
                      />
                    )}
                    {(f.help || (kind === "lists" && f.key === "dialect")) && (
                      <small className="text-xs font-normal leading-relaxed text-muted-foreground">
                        {kind === "lists" && f.key === "dialect"
                          ? listFormatHelp[text(editing.dialect)]
                          : f.help}
                      </small>
                    )}
                  </label>
                ))}
              </div>
              {kind === "rules" && (
                <div className="my-3 rounded-[5px] border border-border border-l-[3px] border-l-[#b69860] bg-muted px-3.5 py-3 text-xs [overflow-wrap:anywhere]">
                  {editing.kind === "exact"
                    ? `Only ${editing.pattern || "the exact name"} matches; descendants do not.`
                    : editing.kind === "suffix"
                      ? `Includes ${editing.pattern || "the apex"} and all descendants.`
                      : "Use the rule tester to inspect wildcard/regex precedence and matching."}
                </div>
              )}
              {kind === "lists" && Number(original?.__references) > 0 && (
                <p className="text-xs text-muted-foreground">
                  This subscription has {String(original?.__references)}{" "}
                  explicit device or profile assignments. Reset those list
                  choices in{" "}
                  <a href="/clients" className="underline">
                    Devices
                  </a>{" "}
                  or{" "}
                  <a href="/profiles" className="underline">
                    Profiles
                  </a>{" "}
                  before deleting it.
                </p>
              )}
              {error && (
                <ErrorNotice
                  error={error}
                  retry={() => {
                    setEditing(undefined);
                    setTick((t) => t + 1);
                    setError(undefined);
                  }}
                />
              )}
              <div
                className={`flex flex-wrap items-center gap-2 ${kind === "lists" ? "justify-end border-t border-border pt-5" : ""}`}
              >
                {kind === "lists" && (
                  <Button
                    type="button"
                    variant="outline"
                    disabled={busy}
                    onClick={() => setEditing(undefined)}
                  >
                    Cancel
                  </Button>
                )}
                <Button disabled={busy || !editRevision}>
                  {busy
                    ? kind === "lists" && editing.enabled === true
                      ? "Downloading…"
                      : "Saving…"
                    : kind === "lists" && !original
                      ? "Add list"
                      : "Save changes"}
                </Button>
                {original && (
                  <Button
                    type="button"
                    variant="destructive"
                    disabled={
                      busy ||
                      (kind === "lists" && Number(original?.__references) > 0)
                    }
                    onClick={() => mutate("DELETE")}
                  >
                    Delete {kind.slice(0, -1)}
                  </Button>
                )}
              </div>
            </form>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}
function RuleTester() {
  const [name, setName] = useState("");
  const [qtype, setQtype] = useState("A");
  const [result, setResult] = useState<Row>();
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  return (
    <section className="mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background p-5">
      <h2 className="mb-3 text-sm font-medium">Rule tester</h2>
      <form
        className="flex min-w-0 flex-wrap items-end gap-3 [&>*]:min-w-0"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError(undefined);
          setResult(undefined);
          try {
            setResult(
              await api.send<Row>("rules/test", "POST", { name, qtype }),
            );
          } catch (err) {
            setError(err as Error);
          } finally {
            setBusy(false);
          }
        }}
      >
        <label className="flex min-w-0 basis-40 flex-1 flex-col gap-1.5 text-xs font-normal">
          Domain
          <Input
            required
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </label>
        <label className="flex min-w-0 basis-40 flex-1 flex-col gap-1.5 text-xs font-normal">
          Type
          <select
            className="min-h-9 w-full min-w-0 rounded-md border border-input bg-background px-2.5 py-2 text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
            value={qtype}
            onChange={(e) => setQtype(e.target.value)}
          >
            {["A", "AAAA", "HTTPS", "MX", "TXT", "PTR"].map((t) => (
              <option key={t}>{t}</option>
            ))}
          </select>
        </label>
        <Button disabled={busy}>{busy ? "Testing…" : "Test rule"}</Button>
      </form>
      {error && <ErrorNotice error={error} />}{" "}
      {result && (
        <div
          className="my-3 rounded-[5px] border border-border border-l-[3px] border-l-[#b69860] bg-muted px-3.5 py-3 text-xs [overflow-wrap:anywhere]"
          role="status"
        >
          <strong>
            {(
              {
                block: "Blocked",
                allow: "Allowed by a rule",
                forward: "Allowed — sent to an upstream server",
                paused: "Allowed — blocking is paused",
                local: "Answered by local DNS",
              } as Record<string, string>
            )[String((result.decision as Row)?.result)] ?? "Test completed"}
          </strong>
          <p className="mt-1 mb-2">{text(result.normalized || result.name)}</p>
          <details className="min-w-0 border-t border-border px-5 py-3 text-xs">
            <summary className="cursor-pointer text-muted-foreground">
              Match details
            </summary>
            <Details value={result} />
          </details>
        </div>
      )}
    </section>
  );
}
