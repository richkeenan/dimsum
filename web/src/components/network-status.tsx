import { useEffect, useState } from "react";
import { Copy } from "lucide-react";
import { api, type Row, type Settings } from "@/lib/api";
import { useResource } from "@/lib/hooks";
import { Button } from "./ui/button";
import { ErrorNotice } from "./data";

type Diagnostics = {
  dns_ready?: boolean;
  dns_addresses?: string[];
  storage?: { available?: boolean; writer?: { LastError?: string } };
};

export function DNSAddresses() {
  const diagnostics = useResource<Diagnostics>("diagnostics");
  const [feedback, setFeedback] = useState("");
  useEffect(() => {
    if (!feedback) return;
    const timeout = window.setTimeout(() => setFeedback(""), 2500);
    return () => window.clearTimeout(timeout);
  }, [feedback]);

  async function copy(address: string) {
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(address);
      } else {
        // The LAN admin UI can be served over HTTP, without Clipboard API access.
        const input = document.createElement("textarea");
        const focused = document.activeElement;
        input.value = address;
        input.style.position = "fixed";
        input.style.opacity = "0";
        document.body.append(input);
        try {
          input.select();
          if (!document.execCommand("copy")) throw new Error("Copy failed");
        } finally {
          input.remove();
          if (focused instanceof HTMLElement) focused.focus();
        }
      }
      setFeedback("Copied");
    } catch {
      setFeedback("Select the address to copy it manually.");
    }
  }

  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
      <span className="text-muted-foreground">DNS server</span>
      {diagnostics.data?.dns_addresses?.map((endpoint) => {
        const split = endpoint.lastIndexOf(":");
        const address = endpoint.slice(0, split).replace(/^\[|\]$/g, "");
        const port = endpoint.slice(split + 1);
        return (
          <div key={endpoint} className="flex min-w-0 items-center gap-1">
            <code className="select-all wrap-anywhere text-foreground">
              {address}
            </code>
            {port !== "53" && (
              <span className="text-muted-foreground">Port {port}</span>
            )}
            <Button
              variant="ghost"
              size="icon"
              aria-label={`Copy DNS address ${address}`}
              onClick={() => void copy(address)}
            >
              <Copy size={14} />
            </Button>
          </div>
        );
      })}
      {!diagnostics.loading && !diagnostics.data?.dns_addresses?.length && (
        <span className="text-muted-foreground">
          {diagnostics.error
            ? "Address unavailable"
            : "No client-facing address"}
        </span>
      )}
      {feedback && (
        <span role="status" className="text-muted-foreground">
          {feedback}
        </span>
      )}
    </div>
  );
}

export function ServiceNotices() {
  const diagnostics = useResource<Diagnostics>("diagnostics");
  const blocking = useResource<Row>("blocking");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  const storage = diagnostics.data?.storage;
  const notices = [
    diagnostics.data?.dns_ready === false && "DNS is not ready.",
    storage?.available === false
      ? "Statistics are unavailable."
      : storage?.writer?.LastError && "Statistics could not be saved.",
  ].filter(Boolean);

  async function resume() {
    setBusy(true);
    setError(undefined);
    try {
      const settings = await api.get<Settings>("settings");
      await api.send("blocking", "PUT", {
        revision: settings.revision,
        enabled: true,
      });
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      {(diagnostics.error || blocking.error) && (
        <ErrorNotice error={diagnostics.error ?? blocking.error!} />
      )}
      {!!notices.length && (
        <div
          role="alert"
          className="mb-5 rounded-md border border-border border-l-[3px] border-l-destructive bg-muted px-4 py-3 text-xs"
        >
          {notices.join(" ")}
        </div>
      )}
      {blocking.data?.enabled === false && (
        <div className="mb-5 flex flex-wrap items-center justify-between gap-3 rounded-md border border-border bg-muted px-4 py-3 text-xs">
          <p>
            Filtering is paused for all devices.
            {!!blocking.data.pause_until &&
              ` Resumes ${new Date(String(blocking.data.pause_until)).toLocaleString()}.`}
          </p>
          <Button
            variant="outline"
            disabled={busy}
            onClick={() => void resume()}
          >
            Resume filtering
          </Button>
        </div>
      )}
      {error && <ErrorNotice error={error} />}
    </>
  );
}
