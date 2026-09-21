import { useState } from "react";
import { api, text, type Settings } from "@/lib/api";
import { useResource } from "@/lib/hooks";
import { Details, ErrorNotice, Resource } from "@/components/data";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
export function Revision({ value }: { value?: Settings }) {
  return (
    <div className="revision">
      <span>
        Saved revision <code>{value?.revision ?? "Unknown"}</code>
      </span>
      {value?.status?.restart_required && (
        <span className="warning">Restart/rebind required</span>
      )}
      {value?.status?.recovered && (
        <span className="warning">Running last-known-good configuration</span>
      )}
      <span>
        Active generation <b>{text(value?.active_generation)}</b>
      </span>
      <span className={value?.pending ? "warning" : ""}>
        {value?.pending
          ? "Pending activation"
          : value?.error
            ? "Saved configuration has errors; inspect active generation"
            : "Activation status: " +
              (value?.active_generation && value.active_generation !== "0"
                ? "active"
                : "unknown")}
      </span>
      {value?.error && <p role="alert">{value.error}</p>}
    </div>
  );
}
export default function SettingsView() {
  const [tick, setTick] = useState(0);
  const state = useResource<Settings>("settings", tick);
  const [path, setPath] = useState("cache.bytes");
  const [value, setValue] = useState("");
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  async function save() {
    setError(undefined);
    setSaved(false);
    setBusy(true);
    try {
      if (!state.data?.revision)
        throw new Error("Reload settings to obtain the current revision.");
      const parsed = JSON.parse(value);
      if (
        !["string", "boolean", "number"].includes(typeof parsed) ||
        (typeof parsed === "number" && !Number.isSafeInteger(parsed))
      )
        throw new Error(
          "Enter a string, boolean, or safe integer. Edit collection items by their indexed path.",
        );
      await api.edit(state.data.revision, [
        { path: path.split("."), value: parsed },
      ]);
      setSaved(true);
      setTick((t) => t + 1);
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Resource state={state} retry={() => setTick((t) => t + 1)}>
      <Revision value={state.data} />
      <div className="split">
        <section className="panel inset">
          <h2>Edit a configuration value</h2>
          <p>
            Changes update only the selected path in the authoritative text.
            Comments and unrelated formatting are preserved by the service.
          </p>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void save();
            }}
          >
            <label>
              Configuration path
              <Input
                required
                value={path}
                onChange={(e) => setPath(e.target.value)}
                list="setting-paths"
              />
              <datalist id="setting-paths">
                {[
                  "dns.listen.0",
                  "dns.upstreams.0",
                  "dns.fallback_upstreams.0",
                  "dns.upstream_policy.timeout_ms",
                  "admin.listen",
                  "cache.bytes",
                  "cache.stale_mode",
                  "cache.max_stale_seconds",
                  "statistics.detail_days",
                  "statistics.minute_days",
                  "statistics.hour_days",
                  "statistics.day_days",
                  "naming.resolver",
                  "naming.hosts_file",
                ].map((p) => (
                  <option key={p}>{p}</option>
                ))}
              </datalist>
            </label>
            <label>
              New value (JSON)
              <textarea
                required
                rows={5}
                value={value}
                onChange={(e) => setValue(e.target.value)}
                placeholder={'e.g. 8388608 or "immediate"'}
              />
            </label>
            {error && (
              <ErrorNotice
                error={error}
                retry={() => {
                  setTick((t) => t + 1);
                  setError(undefined);
                }}
              />
            )}
            <Button disabled={busy || !state.data?.revision}>
              {busy ? "Saving…" : "Save changes"}
            </Button>
            {saved && (
              <p role="status">
                Saved. Review active generation and any pending activation
                above.
              </p>
            )}
          </form>
        </section>
        <section className="panel inset">
          <h2>Current configuration</h2>
          <p className="muted">Redacted inspection is not a complete backup.</p>
          {state.data?.source ? (
            <pre className="source">{state.data.source}</pre>
          ) : (
            <Details value={state.data?.config} />
          )}
        </section>
      </div>
    </Resource>
  );
}
