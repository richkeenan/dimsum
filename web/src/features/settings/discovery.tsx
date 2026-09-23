import { useEffect, useState } from "react";
import { InfoDetails } from "@/components/info-details";
import { api, type Row, type Settings } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { ErrorNotice } from "@/components/data";

function initial(settings: Settings) {
  const naming = settings.config?.naming as Row | undefined;
  const mdns = naming?.mdns as Row | undefined;
  return {
    enabled: mdns?.enabled === true,
    interfaces: Array.isArray(mdns?.interfaces) ? mdns.interfaces.join("\n") : "",
    revision: settings.revision,
  };
}
export function DiscoveryStatus({ value }: { value?: Row }) {
  if (!value) return null;
  const enabled = value.enabled === true;
  const running = value.running === true;
  const errors = Array.isArray(value.errors) ? value.errors.map(String) : [];
  return (
    <div className="space-y-2 text-xs text-muted-foreground">
      <div className="flex items-center gap-1">
        <p>Discovery: {!enabled ? "Disabled" : running ? "Running" : "Unavailable"}</p>
        <InfoDetails label="Device discovery details">
          <p className="text-muted-foreground">
            Discovery uses mDNS / Bonjour to learn device names on your local network. Devices that
            do not advertise a name may still appear by address.
          </p>
          {errors.length > 0 ? (
            <>
              <p className="mt-3 text-muted-foreground">
                Some discovery attempts encountered problems. These recorded diagnostics may include
                past problems or a failed attempt over one protocol while another worked; they do
                not necessarily mean discovery is unavailable now.
              </p>
              <h4 className="mt-3 mb-2 font-medium">Recorded technical details</h4>
              <ul className="space-y-2">
                {errors.map((error, i) => (
                  <li key={i} className="font-mono text-muted-foreground wrap-anywhere">
                    {error}
                  </li>
                ))}
              </ul>
            </>
          ) : (
            <p className="mt-3 text-muted-foreground">No discovery problems recorded.</p>
          )}
        </InfoDetails>
      </div>
      {enabled && !running && (
        <div className="space-y-2">
          <p>Automatic device naming is unavailable.</p>
          <p>
            Review LAN interfaces in Settings → Device discovery. If you entered interface names,
            leave that field empty and save to use active network connections automatically. If
            discovery remains unavailable, share the technical details with your administrator or
            support.
          </p>
        </div>
      )}
      {Array.isArray(value.interfaces) && value.interfaces.length > 0 && (
        <p>Interfaces: {value.interfaces.join(", ")}</p>
      )}
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
    if (saved && settings.revision !== draft.revision) setDraft(initial(settings));
  }, [saved, settings, draft.revision]);
  return (
    <section className="mb-5 rounded-lg border border-border bg-background p-5">
      <h2 className="mb-3 text-sm font-medium">Device discovery</h2>
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
        <p id="discovery-interface-help" className="text-xs text-muted-foreground">
          Optional: one interface name per line, up to eight. Leave empty to use active
          multicast-capable interfaces.
        </p>
        {outdated && !saved && (
          <p className="text-xs text-muted-foreground">
            Configuration changed. Reload to discard this draft and use the latest revision.
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
      </form>
      <div className="mt-4">
        <DiscoveryStatus value={diagnostics} />
      </div>
    </section>
  );
}
