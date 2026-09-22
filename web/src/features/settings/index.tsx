import { useState } from "react";
import { api, type Row, type Settings } from "@/lib/api";
import { useResource } from "@/lib/hooks";
import { Details, ErrorNotice, Resource } from "@/components/data";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { AgentAccess } from "./agents";
import { DiscoverySettings } from "./discovery";

export function Revision({ value }: { value?: Settings }) {
  if (
    !value?.pending &&
    !value?.error &&
    !value?.status?.restart_required &&
    !value?.status?.recovered
  )
    return null;
  return (
    <div
      className="my-3 rounded-[5px] border border-border border-l-[3px] border-l-[#b69860] bg-muted px-3.5 py-3 text-xs text-[#996a26] [overflow-wrap:anywhere] dark:text-amber-300 [&>p]:mt-1 [&>p]:mb-2"
      role={value?.error ? "alert" : "status"}
    >
      {value?.error ? (
        <p>Changes could not be activated: {value.error}</p>
      ) : value?.pending ? (
        <p>Applying saved changes…</p>
      ) : null}
      {value?.status?.restart_required && (
        <p>Restart the service to apply listener changes.</p>
      )}
      {value?.status?.recovered && <p>Using the last working configuration.</p>}
    </div>
  );
}

type Setting = {
  path: string;
  label: string;
  help?: string;
  options?: [string, string][];
  min?: number;
  type?: "text";
};
const groups: { title: string; fields: Setting[] }[] = [
  {
    title: "DNS",
    fields: [
      {
        path: "dns.listen.0",
        label: "Primary listening address",
        type: "text",
        help: "IP address and port, for example 127.0.0.1:5353. Listener changes require a restart.",
      },
      {
        path: "dns.upstream_policy.timeout_ms",
        label: "Upstream timeout (milliseconds)",
        min: 1,
      },
    ],
  },
  {
    title: "Cache",
    fields: [
      { path: "cache.bytes", label: "Memory budget (bytes)", min: 1 },
      {
        path: "cache.stale_mode",
        label: "Use expired answers",
        options: [
          ["immediate", "Immediately, while refreshing"],
          ["failure-only", "Only if an upstream fails"],
          ["off", "Never"],
        ],
      },
      {
        path: "cache.max_stale_seconds",
        label: "Maximum expired-answer age (seconds)",
        min: 0,
      },
      {
        path: "cache.max_negative_ttl_seconds",
        label: "Cache missing domains for (seconds)",
        min: 0,
      },
    ],
  },
  {
    title: "History",
    fields: [
      { path: "statistics.detail_days", label: "Query details (days)", min: 1 },
      {
        path: "statistics.minute_days",
        label: "Minute summaries (days)",
        min: 1,
      },
      {
        path: "statistics.hour_days",
        label: "Hourly summaries (days)",
        min: 1,
      },
      { path: "statistics.day_days", label: "Daily summaries (days)", min: 1 },
    ],
  },
];
export function settingValue(config: Row | undefined, path: string): string {
  let value: unknown = config;
  for (const key of path.split("."))
    value =
      value && typeof value === "object" ? (value as Row)[key] : undefined;
  return value == null ? "" : String(value);
}

function SettingsForm({
  settings,
  refresh,
}: {
  settings: Settings;
  refresh: () => void;
}) {
  // Capture the original revision with the first edit. Polling must never rebase a draft silently.
  const [draft, setDraft] = useState<Record<string, string>>({});
  const [revision, setRevision] = useState<string>();
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  function change(path: string, value: string) {
    setRevision((current) => current ?? settings.revision);
    setDraft((current) => ({ ...current, [path]: value }));
    setSaved(false);
  }
  return (
    <form
      className="mb-6 min-w-0"
      onSubmit={async (event) => {
        event.preventDefault();
        setBusy(true);
        setError(undefined);
        setSaved(false);
        try {
          if (!revision)
            throw new Error("Wait for settings to load before editing.");
          const edits = Object.entries(draft).map(([path, value]) => {
            const field = groups
              .flatMap((g) => g.fields)
              .find((f) => f.path === path)!;
            const parsed =
              field.type === "text" || field.options ? value : Number(value);
            if (
              typeof parsed === "number" &&
              (!value.trim() ||
                !Number.isSafeInteger(parsed) ||
                parsed < (field.min ?? 0))
            )
              throw new Error(`Enter a valid value for ${field.label}.`);
            return { path: path.split("."), value: parsed };
          });
          await api.edit(revision, edits);
          setDraft({});
          setRevision(undefined);
          setSaved(true);
          refresh();
        } catch (e) {
          setError(e as Error);
        } finally {
          setBusy(false);
        }
      }}
    >
      {groups.map((group) => (
        <section
          className="mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background p-5"
          key={group.title}
        >
          <h2 className="mb-3 text-sm font-medium">{group.title}</h2>
          <div className="mt-3.5 mb-[22px] grid min-w-0 grid-cols-1 gap-4 min-[701px]:grid-cols-2 [&>*]:min-w-0">
            {group.fields.map((field) => (
              <label
                className="flex min-w-0 flex-col gap-1.5 text-xs font-normal"
                key={field.path}
              >
                {field.label}
                {field.options ? (
                  <select
                    className="min-h-9 w-full min-w-0 rounded-md border border-input bg-background px-2.5 py-2 text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:opacity-50"
                    disabled={busy || !settings.revision}
                    value={
                      draft[field.path] ??
                      settingValue(settings.config, field.path)
                    }
                    onChange={(e) => change(field.path, e.target.value)}
                  >
                    {field.options.map(([value, label]) => (
                      <option key={value} value={value}>
                        {label}
                      </option>
                    ))}
                  </select>
                ) : (
                  <Input
                    disabled={busy || !settings.revision}
                    type={field.type ?? "number"}
                    min={field.min}
                    step={field.type ? undefined : 1}
                    value={
                      draft[field.path] ??
                      settingValue(settings.config, field.path)
                    }
                    onChange={(e) => change(field.path, e.target.value)}
                  />
                )}
                {field.help && (
                  <small className="text-xs font-normal text-muted-foreground">
                    {field.help}
                  </small>
                )}
              </label>
            ))}
          </div>
        </section>
      ))}
      {error && <ErrorNotice error={error} />}
      <div className="flex flex-wrap items-center gap-2">
        <Button
          disabled={busy || !settings.revision || !Object.keys(draft).length}
        >
          {busy ? "Saving…" : "Save settings"}
        </Button>
        {!!Object.keys(draft).length && (
          <Button
            type="button"
            variant="outline"
            disabled={busy}
            onClick={() => {
              setDraft({});
              setRevision(undefined);
              setError(undefined);
              refresh();
            }}
          >
            Discard edits and reload
          </Button>
        )}
      </div>
      {saved && (
        <p className="mt-3 text-xs text-muted-foreground" role="status">
          Settings saved.
        </p>
      )}
    </form>
  );
}

export function PasswordForm() {
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  return (
    <section className="mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background p-5">
      <h2 className="mb-3 text-sm font-medium">Change password</h2>
      <p className="mb-[18px] text-xs text-muted-foreground">
        You will be signed out on all devices after changing your password.
      </p>
      <form
        onSubmit={async (event) => {
          event.preventDefault();
          setError(undefined);
          if (password !== confirmation) {
            setError(new Error("The passwords do not match."));
            return;
          }
          setBusy(true);
          try {
            await api.send("password", "POST", { password });
            setPassword("");
            setConfirmation("");
            sessionStorage.removeItem("dimsum-csrf");
            window.dispatchEvent(new Event("session-expired"));
          } catch (e) {
            setError(e as Error);
          } finally {
            setBusy(false);
          }
        }}
      >
        <div className="mt-3.5 mb-[22px] grid min-w-0 grid-cols-1 gap-4 min-[701px]:grid-cols-2 [&>*]:min-w-0">
          <label className="flex min-w-0 flex-col gap-1.5 text-xs font-normal">
            New password
            <Input
              type="password"
              autoComplete="new-password"
              required
              value={password}
              disabled={busy}
              onChange={(e) => setPassword(e.target.value)}
            />
          </label>
          <label className="flex min-w-0 flex-col gap-1.5 text-xs font-normal">
            Confirm new password
            <Input
              type="password"
              autoComplete="new-password"
              required
              value={confirmation}
              disabled={busy}
              onChange={(e) => setConfirmation(e.target.value)}
            />
          </label>
        </div>
        {error && <ErrorNotice error={error} />}
        <Button disabled={busy}>
          {busy ? "Changing password…" : "Change password"}
        </Button>
      </form>
    </section>
  );
}

export default function SettingsView() {
  const [tick, setTick] = useState(0);
  const state = useResource<Settings>("settings", tick);
  const diagnostics = useResource<Row>("diagnostics", tick);
  return (
    <div className="min-w-0 [&_p]:leading-relaxed">
      <Revision value={state.data} />
      {state.data ? (
        <>
          <SettingsForm
            settings={state.data}
            refresh={() => setTick((t) => t + 1)}
          />
          <DiscoverySettings
            settings={state.data}
            refresh={() => setTick((t) => t + 1)}
            diagnostics={diagnostics.data?.naming as Row | undefined}
          />
          {state.error && (
            <ErrorNotice
              error={state.error}
              retry={() => setTick((t) => t + 1)}
            />
          )}
        </>
      ) : (
        <Resource state={state} retry={() => setTick((t) => t + 1)}>
          {null}
        </Resource>
      )}
      <PasswordForm />
      <AgentAccess />
      <details className="mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background p-5 text-xs">
        <summary className="cursor-pointer text-muted-foreground">
          Advanced configuration details
        </summary>
        <p className="mt-2.5 mb-[18px] max-w-[75ch] text-xs text-muted-foreground">
          Redacted configuration for troubleshooting. Use a backup to export the
          complete configuration.
        </p>
        {state.data?.source ? (
          <pre className="max-h-[600px] overflow-auto font-mono text-xs whitespace-pre-wrap [overflow-wrap:anywhere]">
            {state.data.source}
          </pre>
        ) : (
          <Details value={state.data?.config} />
        )}
      </details>
    </div>
  );
}
