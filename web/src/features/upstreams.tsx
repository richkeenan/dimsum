import { useEffect, useState } from "react";
import { ArrowDown, ArrowUp } from "lucide-react";
import { api, APIError, type Job, type Row } from "@/lib/api";
import { ErrorNotice } from "@/components/data";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";

const providers = [
  {
    id: "cloudflare",
    name: "Cloudflare",
    description:
      "General-purpose DNS without provider-level content filtering.",
    addresses: ["1.1.1.1:53", "1.0.0.1:53"],
    encrypted: "https://cloudflare-dns.com/dns-query",
  },
  {
    id: "google",
    name: "Google Public DNS",
    description:
      "General-purpose DNS without provider-level content filtering.",
    addresses: ["8.8.8.8:53", "8.8.4.4:53"],
    encrypted: "https://dns.google/dns-query",
  },
  {
    id: "quad9",
    name: "Quad9",
    description:
      "Blocks known malicious domains in addition to your dimsum rules.",
    addresses: ["9.9.9.9:53", "149.112.112.112:53"],
    encrypted: "https://dns.quad9.net/dns-query",
  },
];

function encryptedTransport(address: string) {
  return /^(https|tls):\/\//i.exec(address)?.[1].toLowerCase();
}

function isEncrypted(address: string) {
  return encryptedTransport(address) !== undefined;
}

export function UpstreamPoolSummary({ config }: { config?: Row }) {
  const dns = config?.dns as Row | undefined;
  const addresses = [
    ...((dns?.upstreams as string[]) ?? []),
    ...((dns?.fallback_upstreams as string[]) ?? []),
  ];
  if (!addresses.length) return null;
  const encrypted = addresses.filter(isEncrypted).length;
  return (
    <p className="mb-4 rounded-md border border-border bg-muted/40 p-3 text-xs text-muted-foreground">
      {encrypted === addresses.length
        ? "All configured upstreams use encrypted DNS. Test connections below to check that they respond."
        : encrypted > 0
          ? "Mixed connections: some primary or fallback servers use standard DNS. Some lookups may be sent unencrypted."
          : "Configured upstreams use standard DNS (unencrypted)."}
      {" Encryption covers the connection from dimsum to the upstream server."}
    </p>
  );
}

export function UpstreamSelection({
  mode,
  disabled,
  saving,
  change,
}: {
  mode?: string;
  disabled: boolean;
  saving: boolean;
  change: (mode: string) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-6 gap-y-3 border-b border-border px-[18px] py-4">
      <label className="flex flex-col gap-1.5 text-xs">
        Selection mode
        <select
          className="min-h-9 min-w-40 rounded-md border border-input bg-background px-2.5 py-2 text-sm text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:opacity-50"
          value={mode ?? ""}
          disabled={disabled}
          aria-describedby="upstream-selection-help"
          onChange={(event) => change(event.target.value)}
        >
          {!mode && <option value="">Loading…</option>}
          <option value="ordered">Ordered</option>
          <option value="adaptive">Adaptive</option>
        </select>
      </label>
      <p
        id="upstream-selection-help"
        className="max-w-xl text-xs text-muted-foreground"
      >
        {mode === "ordered"
          ? "Try servers from top to bottom, skipping unhealthy servers. Use the arrows to change their priority."
          : mode === "adaptive"
            ? "Prefer servers with lower measured latency, occasionally trying others. Your saved order is kept for Ordered mode."
            : "Loading the saved selection mode."}
      </p>
      {saving && (
        <span role="status" className="text-xs text-muted-foreground">
          Saving…
        </span>
      )}
    </div>
  );
}

export function UpstreamOrder({
  address,
  index,
  count,
  disabled,
  move,
}: {
  address: string;
  index: number;
  count: number;
  disabled: boolean;
  move: (index: number, direction: number) => void;
}) {
  return (
    <div className="flex items-center gap-1">
      <span className="mr-2 min-w-4 text-xs text-muted-foreground tabular-nums">
        {index + 1}
      </span>
      {([-1, 1] as const).map((direction) => {
        const label = direction === -1 ? "up" : "down";
        const Icon = direction === -1 ? ArrowUp : ArrowDown;
        return (
          <Button
            key={direction}
            size="icon"
            variant="outline"
            aria-label={`Move ${address} ${label}`}
            title={`Move ${label}`}
            disabled={
              disabled || index + direction < 0 || index + direction >= count
            }
            onClick={() => move(index, direction)}
          >
            <Icon aria-hidden="true" />
          </Button>
        );
      })}
    </div>
  );
}

export function UpstreamName({ address }: { address: string }) {
  const transport = encryptedTransport(address);
  const provider = providers.find(
    (p) => p.addresses.includes(address) || p.encrypted === address,
  );
  return (
    <div className="flex flex-col gap-1">
      <span>{provider?.name ?? "Custom DNS server"}</span>
      <span className="max-w-80 whitespace-normal text-xs text-muted-foreground tabular-nums [overflow-wrap:anywhere]">
        {address}
      </span>
      <span className="text-xs text-muted-foreground">
        {transport === "https"
          ? "Encrypted · HTTPS (DoH)"
          : transport === "tls"
            ? "Encrypted · TLS (DoT)"
            : "Standard · unencrypted"}
      </span>
    </div>
  );
}

function splitEndpoint(address: string) {
  const split = address.lastIndexOf(":");
  return {
    ip: address.slice(0, split).replace(/^\[|\]$/g, ""),
    port: address.slice(split + 1),
  };
}

class UpstreamInputError extends Error {
  constructor(
    public field: "ip" | "port" | "url",
    message: string,
  ) {
    super(message);
  }
}

function encryptedEndpoint(value: string) {
  const address = value.trim();
  try {
    const url = new URL(address);
    if (
      !isEncrypted(address) ||
      !url.hostname ||
      url.username ||
      url.password ||
      url.hash ||
      /\s/.test(address) ||
      (url.protocol === "tls:" && (url.pathname || url.search))
    )
      throw new Error();
  } catch {
    throw new UpstreamInputError(
      "url",
      "Enter an https:// URL for DNS over HTTPS or tls://host[:port] for DNS over TLS, without credentials or fragments.",
    );
  }
  return address;
}

function endpoint(ip: string, port: string): string {
  const host = ip.trim().replace(/^\[|\]$/g, "");
  const invalid =
    "Enter an IP address, such as 8.8.8.8 or 2001:db8::53. URLs and hostnames are not supported.";
  let canonical = host;
  if (host.includes(":")) {
    try {
      const scope = host.indexOf("%");
      const address = scope < 0 ? host : host.slice(0, scope);
      const zone = scope < 0 ? "" : host.slice(scope + 1);
      if (scope >= 0 && (!zone || /[\s\[\]]/.test(zone)))
        throw new Error(invalid);
      canonical = new URL(`http://[${address}]/`).hostname;
      if (zone) canonical = `${canonical.slice(0, -1)}%${zone}]`;
    } catch {
      throw new UpstreamInputError("ip", invalid);
    }
  } else if (
    !/^(0|[1-9]\d{0,2})(\.(0|[1-9]\d{0,2})){3}$/.test(host) ||
    host.split(".").some((part) => Number(part) > 255)
  ) {
    throw new UpstreamInputError("ip", invalid);
  }
  if (!/^\d+$/.test(port) || Number(port) < 1 || Number(port) > 65535)
    throw new UpstreamInputError(
      "port",
      "Enter a port from 1 to 65535. Standard DNS uses port 53.",
    );
  return `${canonical}:${Number(port)}`;
}

function saveError(error: Error): Error {
  let message = error.message;
  if (error instanceof APIError && error.status === 409)
    message =
      "The configuration changed while you were editing. Close this form, refresh the page, and try again.";
  else if (/points to DNS listener/.test(message))
    message =
      "This address points back to dimsum. Choose another DNS server to avoid a lookup loop.";
  else if (/at most 16/.test(message))
    message =
      "You can configure up to 16 upstream servers. Remove a server before adding another provider.";
  else if (/expected unicast literal IP/.test(message))
    message =
      "Enter a unicast IP address and a port from 1 to 65535 for standard DNS.";
  else if (
    /block sequence|block mapping|editable shape|explicit text edit/.test(
      message,
    )
  )
    message =
      "This upstream list uses a configuration format the editor cannot change. Put each server on its own list line in the configuration, then try again.";
  if (message === error.message) return error;
  return new APIError(
    error instanceof APIError ? error.status : 400,
    error instanceof APIError ? error.code : "upstream_error",
    message,
    {
      original_message: error.message,
      ...(error instanceof APIError ? { fields: error.fields } : {}),
    },
    error instanceof APIError ? error.requestID : undefined,
  );
}

export function UpstreamEditor({
  original,
  revision,
  configured,
  close,
  saved,
}: {
  original?: Row;
  revision: string;
  configured: Row[];
  close: () => void;
  saved: (result: Row) => void;
}) {
  const originalEncrypted = isEncrypted(String(original?.address ?? ""));
  const initial =
    original && !originalEncrypted
      ? splitEndpoint(String(original.address))
      : { ip: "", port: "53" };
  const [provider, setProvider] = useState(original ? "custom" : "");
  const [transport, setTransport] = useState<"https" | "plain">(
    original && !originalEncrypted ? "plain" : "https",
  );
  const [url, setURL] = useState(
    originalEncrypted ? String(original?.address) : "",
  );
  const [ip, setIP] = useState(initial.ip);
  const [port, setPort] = useState(initial.port);
  const [error, setError] = useState<Error>();
  const [invalid, setInvalid] = useState<"ip" | "port" | "url">();
  const [busy, setBusy] = useState(false);
  const preset = providers.find((p) => p.id === provider);
  const addresses = preset
    ? transport === "https"
      ? [preset.encrypted]
      : preset.addresses
    : [];
  const missing =
    addresses.filter(
      (address) => !configured.some((r) => r.address === address),
    ) ?? [];
  async function submit(remove = false) {
    setError(undefined);
    setInvalid(undefined);
    let address = "";
    if (!remove && !preset) {
      try {
        address =
          transport === "https" ? encryptedEndpoint(url) : endpoint(ip, port);
        if (
          configured.some(
            (r) => r.address === address && r.__index !== original?.__index,
          )
        )
          throw new Error(
            "This server is already configured. Choose a different server address.",
          );
      } catch (e) {
        setError(e as Error);
        setInvalid(
          e instanceof UpstreamInputError
            ? e.field
            : transport === "https"
              ? "url"
              : "ip",
        );
        return;
      }
    }
    setBusy(true);
    try {
      if (original && !remove && address === original.address) {
        close();
        return;
      }
      const result = await api.send<Row>(
        "upstreams",
        remove ? "DELETE" : original ? "PATCH" : "POST",
        {
          revision,
          ...(remove
            ? { index: original?.__index }
            : original
              ? {
                  edits: [{ path: [String(original.__index)], value: address }],
                }
              : { item: preset ? { preset: preset.id, transport } : address }),
        },
      );
      saved(result);
    } catch (e) {
      setError(saveError(e as Error));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) close();
      }}
    >
      <DialogContent className="max-h-[calc(100dvh-32px)] w-[calc(100vw-32px)] min-w-0 overflow-y-auto sm:max-w-[560px] sm:p-7 [&>*]:min-w-0">
        <DialogTitle>{original ? "Edit upstream" : "Add upstream"}</DialogTitle>
        <DialogDescription>
          {original
            ? "Update this server’s address and connection type."
            : "Choose a DNS provider, or use your own server. dimsum sends lookups here when it cannot answer locally."}
        </DialogDescription>
        <form
          className="space-y-5"
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <fieldset disabled={busy} className="min-w-0 space-y-5">
            {!original && (
              <div className="space-y-2">
                <label htmlFor="upstream-provider" className="block text-xs">
                  DNS provider
                </label>
                <select
                  id="upstream-provider"
                  className="min-h-10 w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                  value={provider}
                  onChange={(e) => {
                    setProvider(e.target.value);
                    setError(undefined);
                    setInvalid(undefined);
                  }}
                >
                  <option value="" disabled>
                    Choose a provider…
                  </option>
                  {providers.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                  <option value="custom">Custom DNS server (advanced)</option>
                </select>
              </div>
            )}
            {provider && (
              <fieldset className="space-y-2">
                <legend className="mb-2 text-xs">Connection</legend>
                <div className="grid gap-2 sm:grid-cols-2">
                  {(
                    [
                      [
                        "https",
                        "Encrypted",
                        "Private connection to the server.",
                      ],
                      [
                        "plain",
                        "Standard (unencrypted)",
                        "Uses a plain IP address and port.",
                      ],
                    ] as const
                  ).map(([value, label, help]) => (
                    <label
                      key={value}
                      className={`flex cursor-pointer items-start gap-3 rounded-md border p-3 text-sm ${transport === value ? "border-ring bg-muted/50" : "border-border"}`}
                    >
                      <input
                        type="radio"
                        name="upstream-transport"
                        value={value}
                        checked={transport === value}
                        onChange={() => {
                          setTransport(value);
                          setError(undefined);
                          setInvalid(undefined);
                        }}
                        className="mt-1 accent-primary"
                      />
                      <span>
                        {label}
                        <span className="mt-1 block text-xs text-muted-foreground">
                          {help}
                        </span>
                      </span>
                    </label>
                  ))}
                </div>
              </fieldset>
            )}
            {preset && (
              <section className="space-y-3 rounded-md border border-border bg-muted/40 p-4 text-sm">
                <p>{preset.description}</p>
                <ul className="space-y-2">
                  {addresses.map((address) => (
                    <li
                      key={address}
                      className="flex flex-wrap justify-between gap-2"
                    >
                      <span className="min-w-0 tabular-nums [overflow-wrap:anywhere]">
                        {address}
                      </span>
                      <span className="text-xs text-muted-foreground">
                        {missing.includes(address)
                          ? "Will be added"
                          : "Already configured"}
                      </span>
                    </li>
                  ))}
                </ul>
              </section>
            )}
            {provider === "custom" && transport === "https" && (
              <div className="space-y-2">
                <label htmlFor="upstream-url" className="block text-xs">
                  Encrypted server URL
                </label>
                <Input
                  id="upstream-url"
                  autoComplete="off"
                  spellCheck={false}
                  value={url}
                  placeholder="https://resolver.example/dns-query"
                  aria-invalid={invalid === "url"}
                  aria-describedby={
                    invalid === "url"
                      ? "upstream-validation upstream-url-help"
                      : "upstream-url-help"
                  }
                  onChange={(e) => {
                    setURL(e.target.value);
                    setError(undefined);
                    setInvalid(undefined);
                  }}
                />
                <p
                  id="upstream-url-help"
                  className="text-xs text-muted-foreground"
                >
                  Use https://host/path (DoH) or tls://host[:port] (DoT). Server
                  certificates are verified. Hostnames are resolved
                  automatically; bootstrap overrides are in Settings → Advanced.
                </p>
              </div>
            )}
            {provider === "custom" && transport === "plain" && (
              <div className="space-y-2">
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-[minmax(0,1fr)_100px]">
                  <div className="space-y-2">
                    <label htmlFor="upstream-ip" className="block text-xs">
                      IP address
                    </label>
                    <Input
                      id="upstream-ip"
                      autoComplete="off"
                      spellCheck={false}
                      placeholder="e.g. 8.8.8.8"
                      value={ip}
                      aria-invalid={invalid === "ip"}
                      aria-describedby={
                        invalid === "ip"
                          ? "upstream-validation upstream-address-help"
                          : "upstream-address-help"
                      }
                      onChange={(e) => {
                        setIP(e.target.value);
                        setInvalid(undefined);
                        setError(undefined);
                      }}
                    />
                  </div>
                  <div className="space-y-2">
                    <label htmlFor="upstream-port" className="block text-xs">
                      Port
                    </label>
                    <Input
                      id="upstream-port"
                      type="number"
                      min="1"
                      max="65535"
                      value={port}
                      aria-invalid={invalid === "port"}
                      aria-describedby={
                        invalid === "port"
                          ? "upstream-validation upstream-address-help"
                          : "upstream-address-help"
                      }
                      onChange={(e) => {
                        setPort(e.target.value);
                        setInvalid(undefined);
                        setError(undefined);
                      }}
                    />
                  </div>
                </div>
                <p
                  id="upstream-address-help"
                  className="text-xs leading-relaxed text-muted-foreground"
                >
                  IPv4 or IPv6 address. Standard DNS uses port 53. For a URL,
                  choose Encrypted.
                </p>
              </div>
            )}
          </fieldset>
          {provider && (
            <p className="text-xs text-muted-foreground">
              Save first, then test the connection from the upstream list.
              Adding a server keeps your existing servers.
            </p>
          )}
          {error &&
            (invalid ? (
              <p
                id="upstream-validation"
                role="alert"
                className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive"
              >
                {error.message}
              </p>
            ) : (
              <ErrorNotice error={error} />
            ))}
          <div className="flex flex-wrap items-center justify-end gap-2 border-t border-border pt-4">
            {original && (
              <Button
                type="button"
                variant="outline"
                className="mr-auto text-destructive"
                disabled={busy}
                onClick={() => void submit(true)}
              >
                Remove server
              </Button>
            )}
            <Button
              type="button"
              variant="outline"
              disabled={busy}
              onClick={close}
            >
              Cancel
            </Button>
            <Button
              disabled={
                busy ||
                !revision ||
                !provider ||
                (!!preset && missing.length === 0)
              }
            >
              {busy
                ? "Saving…"
                : original
                  ? "Save changes"
                  : preset
                    ? missing.length === 0
                      ? "Already configured"
                      : "Add provider"
                    : "Add server"}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function UpstreamConnectionTest({ address }: { address: string }) {
  const [job, setJob] = useState<Job>();
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState<Error>();
  useEffect(() => {
    if (!job || job.state !== "running") return;
    const abort = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      try {
        const response = await api.get<{ items: Job[] }>("jobs", abort.signal);
        if (abort.signal.aborted) return;
        const latest = response.items.find((item) => item.id === job!.id);
        if (!latest)
          throw new Error(
            "The connection test is no longer available. Run it again.",
          );
        if (latest.state !== "running") setJob(latest);
        else timer = setTimeout(poll, 500);
      } catch (e) {
        if (!abort.signal.aborted) {
          setError(e as Error);
          setJob(undefined);
        }
      }
    }
    void poll();
    return () => {
      abort.abort();
      clearTimeout(timer);
    };
  }, [job]);
  const result = job?.result as Row | undefined;
  const running = starting || job?.state === "running";
  let message = "";
  if (job?.state === "failed")
    message = `The test could not finish. ${job.error || "Try again."}`;
  else if (job?.state === "succeeded")
    message =
      result?.healthy === true
        ? `${result.transport === "https" || result.transport === "tls" ? "Encrypted connection verified · " : ""}Responded in ${(Number(result.duration_us) / 1000).toLocaleString(undefined, { minimumFractionDigits: 1, maximumFractionDigits: 1 })} ms${result.transport ? ` · ${String(result.transport).toUpperCase()}` : ""}`
        : result?.responding === true
          ? "Server responded with a DNS error. Try another server."
          : "No valid response. Check the server address and network connection.";
  async function start() {
    setStarting(true);
    setError(undefined);
    setJob(undefined);
    try {
      setJob(
        await api.send<Job>("jobs", "POST", {
          kind: "upstream-probe",
          input: { endpoint: address },
        }),
      );
    } catch (e) {
      setError(e as Error);
    } finally {
      setStarting(false);
    }
  }
  return (
    <div className="max-w-64 whitespace-normal text-xs">
      <Button
        size="sm"
        variant="outline"
        disabled={running}
        onClick={() => void start()}
      >
        {running ? "Testing…" : "Test connection"}
      </Button>
      {message && (
        <div className="mt-2 space-y-1" role="status">
          <p>{message}</p>
          {result?.healthy !== true &&
            typeof result?.transport === "string" && (
              <p>
                Configured / attempted transport:{" "}
                {result.transport.toUpperCase()}
              </p>
            )}
          {result?.healthy !== true && typeof result?.error === "string" && (
            <p className="[overflow-wrap:anywhere]">
              {result.error}
              {/bootstrap/i.test(result.error)
                ? " Check bootstrap DNS in Settings → Advanced."
                : /certificate|x509/i.test(result.error)
                  ? " Check the server hostname, certificate and system clock."
                  : ""}
            </p>
          )}
        </div>
      )}
      {error && <ErrorNotice error={error} />}
    </div>
  );
}
