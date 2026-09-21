import { useState } from "react";
import {
  api,
  rows,
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
      help: "Domains: one domain per line. Hosts: IP and domain pairs. DNS adblock: domain-only blocking syntax such as ||example.com^. Browser filter rules are not supported.",
    },
    { key: "domain_kind", label: "Domain scope", options: ["exact", "suffix"] },
    { key: "enabled", label: "Enabled", type: "boolean" },
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
  lists: ["label", "enabled", "rules"],
  rules: ["pattern", "action", "kind", "enabled"],
  records: ["name", "type", "value", "ttl"],
  upstreams: ["address"],
  clients: ["name", "address"],
};
const descriptions: Record<string, string> = {
  lists:
    "Block unwanted domains with a trusted list or add your own subscription.",
  rules:
    "Always block or allow a domain. Choose whether the rule also covers subdomains.",
  records:
    "Give devices and services on your network an easy-to-remember name.",
  upstreams:
    "DNS servers used when an answer is not available locally. Test a server with Probe.",
  clients:
    "Name devices to make their activity easier to recognise. Some routers share one address across several devices.",
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
  return defaults;
}
function CatalogPicker({ choose }: { choose: (item: Row) => void }) {
  const catalog = useResource<Row>("catalog");
  const [selected, setSelected] = useState<Row>();
  return (
    <Resource state={catalog}>
      <label className="flex min-w-0 flex-col gap-1.5 text-xs font-normal">
        Start with a list
        <select
          className="min-h-9 w-full min-w-0 rounded-md border border-input bg-background px-2.5 py-2 text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
          defaultValue=""
          onChange={(e) => {
            const item = rows(catalog.data).find(
              (r) => r.id === e.target.value,
            );
            setSelected(item);
            if (item) choose(item);
          }}
        >
          <option value="">Custom URL</option>
          {rows(catalog.data).map((item) => (
            <option
              key={text(item.id)}
              value={text(item.id)}
              disabled={item.available !== true}
            >
              {text(item.label)}
              {item.available !== true
                ? ` — ${text(item.unavailable_reason)}`
                : ""}
            </option>
          ))}
        </select>
        <small className="text-xs font-normal text-muted-foreground">
          {selected?.description
            ? text(selected.description)
            : "Choose a preset to fill in its URL and format, or enter your own below."}
        </small>
      </label>
    </Resource>
  );
}
function ListName({ row }: { row: Row }) {
  const catalog = useResource<Row>("catalog");
  const preset = rows(catalog.data).find((item) => item.url === row.url);
  let label = text(row.url);
  try {
    const url = new URL(String(row.url));
    label = `${url.hostname}${url.pathname === "/" ? "" : url.pathname}`;
  } catch {
    /* Keep the returned URL readable even if it cannot be parsed. */
  }
  return <span>{preset ? text(preset.label) : label}</span>;
}
export default function Configuration({
  kind,
  range,
}: {
  kind: string;
  range: string;
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
  const [notice, setNotice] = useState<Row>();
  const revision = normalizeSettings(state.data).revision;
  const ready = !!revision && !state.loading && !state.error;
  const configuredClients = collectionRows(state.data);
  const observedClients = rows(state.data?.observed);
  const devices = [
    ...observedClients.map((observed) => ({
      ...observed,
      ...configuredClients.find(
        (client) => client.address === observed.address,
      ),
    })),
    ...configuredClients.filter(
      (client) =>
        !observedClients.some(
          (observed) => observed.address === client.address,
        ),
    ),
  ];
  function open(row?: Row) {
    if (!ready) return;
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
        setEditing(undefined);
        return;
      }
      const result = await api.send<Row>(kind, method, body);
      setNotice(result);
      setEditing(undefined);
      setTick((t) => t + 1);
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  async function operation(path: string, body: Row) {
    setError(undefined);
    setBusy(true);
    try {
      setNotice(await api.send<Row>(path, "POST", body));
      setTick((t) => t + 1);
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="min-w-0 [&_p]:leading-relaxed">
      <p className="mb-5 text-xs text-muted-foreground">{descriptions[kind]}</p>
      <Resource state={settings}>
        <Revision value={settings.data} />
      </Resource>
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
                        <span>
                          {text(r.name || r.address)}
                          {r.name && r.name !== r.address ? (
                            <small className="mt-0.5 block text-xs text-muted-foreground">
                              {text(r.address)}
                            </small>
                          ) : null}
                        </span>
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
                    { key: "count", label: "Queries" },
                    { key: "blocked", label: "Blocked" },
                    {
                      key: "actions",
                      label: "Actions",
                      render: (r) => (
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
              {kind === "clients"
                ? "Configured friendly names"
                : "Configured " + kind}
            </h2>
            <div className="flex flex-wrap items-center gap-2">
              {kind === "lists" && (
                <Button
                  variant="outline"
                  disabled={busy}
                  onClick={() => operation("jobs", { kind: "refresh" })}
                >
                  Refresh lists
                </Button>
              )}
              <Button disabled={!ready} onClick={() => open()}>
                {kind === "clients"
                  ? "Name an address"
                  : "Add " +
                    (kind === "upstreams" ? "upstream" : kind.slice(0, -1))}
              </Button>
            </div>
          </div>
          <Resource state={state} retry={() => setTick((t) => t + 1)}>
            <DataTable
              items={collectionRows(state.data).map((r) => {
                if (kind !== "lists") return r;
                const source = rows(state.data?.status, "sources").find(
                  (s) => s.id === r.id,
                );
                return {
                  ...r,
                  active_enabled: source?.enabled,
                  usable: source?.usable,
                  rules: source?.rules,
                  sha256: source?.sha256,
                  error: source?.error,
                };
              })}
              columns={[
                ...columns[kind].map((key) => ({
                  key,
                  label:
                    (
                      {
                        label: "List",
                        enabled: "Status",
                        rules: "Domains",
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
                    key === "label" ? (
                      <ListName row={r} />
                    ) : key === "enabled" ? (
                      r.enabled === true ? (
                        r.error ? (
                          "Refresh failed"
                        ) : kind === "lists" && r.usable === false ? (
                          "Waiting for refresh"
                        ) : (
                          "Enabled"
                        )
                      ) : r.enabled === false ? (
                        "Disabled"
                      ) : (
                        "Unknown"
                      )
                    ) : (
                      (optionLabels[String(r[key])] ?? text(r[key]))
                    ),
                })),
                {
                  key: "actions",
                  label: "Actions",
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
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={busy}
                          onClick={() =>
                            operation("jobs", {
                              kind: "upstream-probe",
                              input: { endpoint: r.address },
                            })
                          }
                        >
                          Probe
                        </Button>
                      )}
                      {kind === "lists" && (
                        <details className="min-w-0 border-t border-border px-5 py-3 text-xs">
                          <summary className="cursor-pointer text-muted-foreground">
                            Details
                          </summary>
                          <Details
                            value={{
                              URL: r.url,
                              format: r.dialect,
                              error: r.error,
                              ID: r.id,
                              checksum: r.sha256,
                            }}
                          />
                        </details>
                      )}
                    </div>
                  ),
                },
              ]}
              empty={"No " + kind + " returned by the service."}
            />
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
          <details className="min-w-0 border-t border-border px-5 py-3 text-xs">
            <summary className="cursor-pointer text-muted-foreground">
              Technical details
            </summary>
            <Details value={notice} />
          </details>
        </section>
      )}
      {kind === "rules" && <RuleTester />}
      <Dialog
        open={!!editing}
        onOpenChange={(open) => {
          if (!open && !busy) setEditing(undefined);
        }}
      >
        <DialogContent className="max-h-[calc(100dvh-32px)] w-[calc(100vw-32px)] min-w-0 overflow-y-auto [&>*]:min-w-0">
          <DialogTitle>
            {original ? "Edit" : "Add"}{" "}
            {kind === "clients" ? "friendly name" : kind.slice(0, -1)}
          </DialogTitle>
          <DialogDescription>{descriptions[kind]}</DialogDescription>
          {editing && (
            <form
              className="min-w-0"
              onSubmit={(e) => {
                e.preventDefault();
                void mutate(original ? "PATCH" : "POST", editing);
              }}
            >
              <div className="mt-3.5 mb-[22px] grid min-w-0 grid-cols-1 gap-4 min-[701px]:grid-cols-2 [&>*]:min-w-0">
                {kind === "lists" && !original && (
                  <CatalogPicker
                    choose={(item) =>
                      setEditing({
                        ...editing,
                        url: item.url,
                        dialect: item.dialect,
                        domain_kind: item.domain_kind,
                        enabled: item.default_enabled ?? true,
                      })
                    }
                  />
                )}
                {fields[kind].map((f) => (
                  <label
                    className="flex min-w-0 flex-col gap-1.5 text-xs font-normal"
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
                    {f.help && (
                      <small className="text-xs font-normal text-muted-foreground">
                        {f.help}
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
              <div className="flex flex-wrap items-center gap-2">
                <Button disabled={busy || !editRevision}>
                  {busy ? "Saving…" : "Save changes"}
                </Button>
                {original && (
                  <Button
                    type="button"
                    variant="destructive"
                    disabled={busy}
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
      <p className="mb-[18px] text-xs text-muted-foreground">
        Evaluate the current active policy using the same matcher as live DNS.
      </p>
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
