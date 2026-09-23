import { useState } from "react";
import { api, type Row, type Settings } from "@/lib/api";
import { ErrorNotice } from "@/components/data";
import { Button } from "@/components/ui/button";

const defaults = ["1.1.1.1:53", "9.9.9.9:53"];

export function BootstrapSettings({
  settings,
  refresh,
}: {
  settings: Settings;
  refresh: () => void;
}) {
  const configured = (settings.config?.dns as Row | undefined)
    ?.bootstrap_dns as string[] | undefined;
  const [draft, setDraft] = useState<string>();
  const [revision, setRevision] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  function change(value: string) {
    setRevision((current) => current ?? settings.revision);
    setDraft(value);
    setError(undefined);
  }
  return (
    <details className="mb-5 min-w-0 rounded-lg border border-border bg-background p-5">
      <summary className="cursor-pointer text-sm font-medium">
        Advanced: bootstrap DNS
      </summary>
      <form
        className="mt-4 space-y-4"
        onSubmit={async (event) => {
          event.preventDefault();
          setError(undefined);
          const servers = (draft ?? "")
            .split("\n")
            .map((line) => line.trim())
            .filter(Boolean);
          if (!servers.length || servers.length > 16) {
            setError(
              new Error(
                "Enter 1 to 16 bootstrap DNS servers, or use automatic defaults.",
              ),
            );
            return;
          }
          if (!revision) return;
          setBusy(true);
          try {
            await api.edit(revision, [
              { path: ["dns", "bootstrap_dns"], value: servers },
            ]);
            setDraft(undefined);
            setRevision(undefined);
            refresh();
          } catch (e) {
            setError(e as Error);
          } finally {
            setBusy(false);
          }
        }}
      >
        <p className="max-w-[75ch] text-xs text-muted-foreground">
          Bootstrap DNS finds the IP addresses of encrypted DNS servers. These
          lookups use standard, unencrypted DNS and only ask for server
          hostnames, not the websites you visit.
        </p>
        <p className="text-xs text-muted-foreground">
          Automatic defaults: 1.1.1.1:53 and 9.9.9.9:53. Override these if your
          network needs different resolvers.
        </p>
        <div className="space-y-2">
          <label htmlFor="bootstrap-servers" className="block text-xs">
            Bootstrap DNS servers
          </label>
          <textarea
            id="bootstrap-servers"
            rows={3}
            className="w-full min-w-0 rounded-md border border-input bg-background p-3 text-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:opacity-50"
            spellCheck={false}
            autoComplete="off"
            aria-describedby="bootstrap-help"
            disabled={busy || !settings.revision}
            value={
              draft ?? (configured?.length ? configured : defaults).join("\n")
            }
            onChange={(event) => change(event.target.value)}
          />
          <p id="bootstrap-help" className="text-xs text-muted-foreground">
            One IP address and port per line, up to 16. For IPv6, use
            [address]:port. Do not use URLs or dimsum’s own listening address.
          </p>
        </div>
        {error && <ErrorNotice error={error} />}
        <div className="flex flex-wrap gap-2">
          <Button disabled={busy || !revision || draft === undefined}>
            {busy ? "Saving…" : "Save bootstrap DNS"}
          </Button>
          <Button
            type="button"
            variant="outline"
            disabled={busy || !settings.revision}
            onClick={() => change(defaults.join("\n"))}
          >
            Use automatic defaults
          </Button>
          {draft !== undefined && (
            <Button
              type="button"
              variant="outline"
              disabled={busy}
              onClick={() => {
                setDraft(undefined);
                setRevision(undefined);
                setError(undefined);
                refresh();
              }}
            >
              Discard edits and reload
            </Button>
          )}
        </div>
      </form>
    </details>
  );
}
