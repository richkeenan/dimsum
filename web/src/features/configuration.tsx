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
import {
  Completeness,
  DataTable,
  Details,
  ErrorNotice,
  Resource,
} from "@/components/data";
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
};
const fields: Record<string, Field[]> = {
  lists: [
    { key: "id", label: "Source ID" },
    { key: "url", label: "Source URL" },
    {
      key: "dialect",
      label: "Format",
      options: ["domains", "hosts", "adblock"],
    },
    { key: "domain_kind", label: "Domain scope", options: ["exact", "suffix"] },
    { key: "enabled", label: "Enabled", type: "boolean" },
  ],
  rules: [
    { key: "id", label: "Rule ID" },
    {
      key: "kind",
      label: "Match type",
      options: ["exact", "suffix", "wildcard", "regex"],
    },
    { key: "action", label: "Action", options: ["deny", "allow"] },
    { key: "pattern", label: "Pattern" },
    { key: "enabled", label: "Enabled", type: "boolean" },
  ],
  records: [
    { key: "name", label: "DNS name" },
    {
      key: "type",
      label: "Record type",
      options: ["A", "AAAA", "CNAME", "PTR", "TXT"],
    },
    { key: "value", label: "Record value" },
    { key: "ttl", label: "TTL (seconds)", type: "number" },
    { key: "auto_ptr", label: "Automatic reverse record", type: "boolean" },
  ],
  upstreams: [{ key: "address", label: "Address and port" }],
  clients: [
    { key: "address", label: "Observed address" },
    { key: "name", label: "Friendly name" },
  ],
};
const columns: Record<string, string[]> = {
  lists: [
    "id",
    "enabled",
    "url",
    "dialect",
    "active_enabled",
    "usable",
    "rules",
    "sha256",
    "error",
  ],
  rules: ["id", "kind", "action", "pattern", "enabled"],
  records: ["name", "type", "value", "ttl"],
  upstreams: ["address"],
  clients: ["name", "address"],
};
const descriptions: Record<string, string> = {
  lists:
    "A failed refresh retains the previous active list. Saved changes may still be compiling.",
  rules:
    "Rules use explicit match types. Exact names never silently include subdomains.",
  records: "Authoritative local records, aliases, and reverse records.",
  upstreams:
    "Primary UDP/TCP resolvers. Fallback addresses and timeout policy are available in Settings. This API does not yet provide an upstream probe.",
  clients:
    "Addresses are DNS-observed identities. Router proxying can combine devices; IPv6 privacy addresses can split one device.",
};
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
  function open(row?: Row) {
    setOriginal(row?.__index === undefined ? undefined : row);
    setEditRevision(normalizeSettings(state.data).revision);
    setEditing(
      row
        ? Object.fromEntries(
            fields[kind].map((f) => [
              f.key,
              row[f.key] ??
                (f.type === "boolean" ? true : (f.options?.[0] ?? "")),
            ]),
          )
        : Object.fromEntries(
            fields[kind].map((f) => [
              f.key,
              f.type === "boolean" ? true : (f.options?.[0] ?? ""),
            ]),
          ),
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
    <>
      <p className="page-description">{descriptions[kind]}</p>
      <Resource state={settings}>
        <Revision value={settings.data} />
      </Resource>
      {kind === "clients" && (
        <Resource state={state}>
          <section className="panel">
            <div className="panel-heading">
              <h2>Observed clients</h2>
              <span>Selected range · at most 200 identities</span>
            </div>
            {state.data?.observed_available === true ? (
              <>
                <Completeness meta={state.data.observed as Row} />
                {(state.data.observed as Row)?.truncated === true && (
                  <p className="notice">
                    The observed client list is truncated. Narrow the time range
                    to inspect more identities.
                  </p>
                )}
                <DataTable
                  items={rows(state.data.observed)}
                  columns={[
                    {
                      key: "name",
                      label: "Client",
                      render: (r) => (
                        <span>
                          {text(r.name || r.address)}
                          <small>{text(r.address)}</small>
                        </span>
                      ),
                    },
                    { key: "name_source", label: "Name source" },
                    {
                      key: "name_fresh",
                      label: "Name freshness",
                      render: (r) =>
                        r.name_fresh === true ? "Current" : "Stale / unknown",
                    },
                    { key: "last_seen", label: "Last seen" },
                    { key: "count", label: "Queries" },
                    { key: "blocked", label: "Blocked" },
                    {
                      key: "actions",
                      label: "Name override",
                      render: (r) => (
                        <Button
                          size="sm"
                          variant="outline"
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
              <p className="empty">Observed client history is unavailable.</p>
            )}
          </section>
        </Resource>
      )}
      {error && !editing && (
        <ErrorNotice error={error} retry={() => setTick((t) => t + 1)} />
      )}
      <section className="panel">
        <div className="panel-heading">
          <h2>
            {kind === "clients"
              ? "Configured friendly names"
              : "Configured " + kind}
          </h2>
          <div className="actions">
            {kind === "lists" && (
              <Button
                variant="outline"
                disabled={busy}
                onClick={() => operation("jobs", { kind: "refresh" })}
              >
                Refresh lists
              </Button>
            )}
            <Button onClick={() => open()}>
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
                label: key.replaceAll("_", " "),
                render: (r: Row) =>
                  key === "enabled"
                    ? r.enabled === true
                      ? "Enabled"
                      : r.enabled === false
                        ? "Disabled"
                        : "Unknown"
                    : text(r[key]),
              })),
              {
                key: "actions",
                label: "Actions",
                render: (r) => (
                  <div className="actions">
                    <Button size="sm" variant="outline" onClick={() => open(r)}>
                      Edit
                    </Button>
                  </div>
                ),
              },
            ]}
            empty={"No " + kind + " returned by the service."}
          />
          {kind === "lists" &&
            rows(state.data).some((r) => r.homepage || r.license) && (
              <div className="inset">
                {rows(state.data).map((r) => (
                  <p key={text(r.id)}>
                    {text(r.id)} · {text(r.license)} · {text(r.homepage)}
                  </p>
                ))}
              </div>
            )}
        </Resource>
      </section>
      {notice && (
        <section className="panel inset" role="status">
          <h3>Operation result</h3>
          <Details value={notice} />
        </section>
      )}
      {kind === "rules" && <RuleTester />}
      <Dialog
        open={!!editing}
        onOpenChange={(open) => {
          if (!open && !busy) setEditing(undefined);
        }}
      >
        <DialogContent>
          <DialogTitle>
            {original ? "Edit" : "Add"}{" "}
            {kind === "clients" ? "friendly name" : kind.slice(0, -1)}
          </DialogTitle>
          <DialogDescription>
            Save against revision {editRevision || "unavailable"}. The service
            validates before activation.
          </DialogDescription>
          {editing && (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                void mutate(original ? "PATCH" : "POST", editing);
              }}
            >
              <div className="form-grid">
                {fields[kind].map((f) => (
                  <label key={f.key}>
                    {f.label}
                    {f.type === "boolean" ? (
                      <select
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
                        aria-label={f.label}
                        value={text(editing[f.key])}
                        onChange={(e) =>
                          setEditing({ ...editing, [f.key]: e.target.value })
                        }
                      >
                        {f.options.map((o) => (
                          <option key={o}>{o}</option>
                        ))}
                      </select>
                    ) : (
                      <Input
                        required={!f.optional}
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
                  </label>
                ))}
              </div>
              {kind === "rules" && (
                <div className="notice">
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
              <div className="actions">
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
    </>
  );
}
function RuleTester() {
  const [name, setName] = useState("");
  const [qtype, setQtype] = useState("A");
  const [result, setResult] = useState<Row>();
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  return (
    <section className="panel inset">
      <h2>Rule tester</h2>
      <p>
        Evaluate the current active policy using the same matcher as live DNS.
      </p>
      <form
        className="inline-form"
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
        <label>
          Domain
          <Input
            required
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </label>
        <label>
          Type
          <select value={qtype} onChange={(e) => setQtype(e.target.value)}>
            {["A", "AAAA", "HTTPS", "MX", "TXT", "PTR"].map((t) => (
              <option key={t}>{t}</option>
            ))}
          </select>
        </label>
        <Button disabled={busy}>{busy ? "Testing…" : "Test rule"}</Button>
      </form>
      {error && <ErrorNotice error={error} />}{" "}
      {result && <Details value={result} />}
    </section>
  );
}
