import { useEffect, useState } from "react";
import { api, type Row, type Settings } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { ErrorNotice } from "@/components/data";

function initial(settings: Settings) {
  const naming = settings.config?.naming as Row | undefined;
  const mdns = naming?.mdns as Row | undefined;
  return {
    enabled: mdns?.enabled === true,
    interfaces: Array.isArray(mdns?.interfaces)
      ? mdns.interfaces.join("\n")
      : "",
    revision: settings.revision,
  };
}
export function DiscoveryStatus({ value }: { value?: Row }) {
  if (!value) return null;
  return (
    <div className="space-y-2 text-xs text-muted-foreground">
      <p>
        Discovery:{" "}
        {value.enabled !== true
          ? "Disabled"
          : value.running === true
            ? "Running"
            : "Unavailable"}
      </p>
      {Array.isArray(value.interfaces) && value.interfaces.length > 0 && (
        <p>Interfaces: {value.interfaces.join(", ")}</p>
      )}
      {Array.isArray(value.errors) &&
        value.errors.map((error, i) => (
          <p key={i} className="text-destructive wrap-anywhere">
            {String(error)}
          </p>
        ))}
    </div>
  );
}
export function DiscoverySettings({
  settings,
  refresh,
  diagnostics,
}: {
  settings: Settings;
  refresh: () => void;
  diagnostics?: Row;
}) {
  const [draft, setDraft] = useState(() => initial(settings));
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const outdated = settings.revision !== draft.revision;
  useEffect(() => {
    if (saved && settings.revision !== draft.revision)
      setDraft(initial(settings));
  }, [saved, settings, draft.revision]);
  return (
    <section className="mb-5 rounded-lg border border-border bg-background p-5">
      <h2 className="mb-3 text-sm font-medium">Device discovery</h2>
      <p className="mb-4 text-xs text-muted-foreground">
        Find device names and types using local mDNS and Bonjour advertisements.
        User-defined names always take priority.
      </p>
      <form
        className="space-y-4"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError(undefined);
          setSaved(false);
          try {
            await api.edit(draft.revision, [
              { path: ["naming", "mdns", "enabled"], value: draft.enabled },
              {
                path: ["naming", "mdns", "interfaces"],
                value: draft.interfaces.split(/\s+/).filter(Boolean),
              },
            ]);
            setSaved(true);
            refresh();
          } catch (e) {
            setError(e as Error);
          } finally {
            setBusy(false);
          }
        }}
      >
        <label className="flex items-center gap-3 text-sm">
          <input
            type="checkbox"
            checked={draft.enabled}
            disabled={busy}
            onChange={(e) => {
              setDraft({ ...draft, enabled: e.target.checked });
              setSaved(false);
            }}
          />
          Discover device names with mDNS / Bonjour
        </label>
        <label className="flex flex-col gap-1.5 text-xs">
          LAN interfaces
          <textarea
            className="min-h-20 w-full rounded-md border border-input bg-background px-3 py-2 text-base"
            value={draft.interfaces}
            disabled={busy}
            onChange={(e) => {
              setDraft({ ...draft, interfaces: e.target.value });
              setSaved(false);
            }}
            aria-describedby="discovery-interface-help"
          />
        </label>
        <p
          id="discovery-interface-help"
          className="text-xs text-muted-foreground"
        >
          Optional: one interface name per line, up to eight. Leave empty to use
          active multicast-capable interfaces.
        </p>
        {outdated && !saved && (
          <p className="text-xs text-muted-foreground">
            Configuration changed. Reload to discard this draft and use the
            latest revision.
          </p>
        )}
        <div className="flex flex-wrap gap-2">
          <Button type="submit" disabled={busy || !draft.revision || outdated}>
            Save discovery settings
          </Button>
          {(outdated || error) && (
            <Button
              type="button"
              variant="outline"
              disabled={busy}
              onClick={async () => {
                setBusy(true);
                setError(undefined);
                setSaved(false);
                try {
                  setDraft(initial(await api.get<Settings>("settings")));
                  refresh();
                } catch (e) {
                  setError(e as Error);
                } finally {
                  setBusy(false);
                }
              }}
            >
              Reload discovery settings
            </Button>
          )}
        </div>
        {error && <ErrorNotice error={error} />}
        {saved && (
          <p role="status" className="text-xs">
            Discovery settings saved.
          </p>
        )}
      </form>
      <div className="mt-4">
        <DiscoveryStatus value={diagnostics} />
      </div>
    </section>
  );
}
