import { useState } from "react";
import type { components } from "@/lib/openapi";
import { api } from "@/lib/api";
import { useResource } from "@/lib/hooks";
import { ErrorNotice, Resource } from "@/components/data";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { DeviceIcon } from "@/components/device-icon";
import { ActivationStatus } from "@/features/clients/policy";
import { DeviceRuleEditor } from "./device-rule-editor";

type Schema = components["schemas"];
type Entry = Schema["DeviceRuleEntry"];
type Mutation = Schema["DeviceRulesMutation"];
type Edit = { entry?: Entry; revision: string; epoch: number };

export function DeviceRules() {
  const state = useResource<Schema["DeviceRulesRead"]>("device-rules");
  const [search, setSearch] = useState("");
  const [edit, setEdit] = useState<Edit>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  const [saved, setSaved] = useState<Schema["Activation"]>();
  const needsReadback = !!saved && saved.saved_revision !== state.data?.revision;
  const disabled = busy || needsReadback || !state.data?.revision;
  const entries = (state.data?.entries ?? []).filter((entry) =>
    `${entry.rule.name} ${entry.rule.id} ${entry.rule.domains.join(" ")}`
      .toLowerCase()
      .includes(search.toLowerCase()),
  );
  async function reload() {
    setError(undefined);
    const result = await state.reload();
    if (result.error || !result.data) {
      setError(result.error ?? new Error("Device rules could not be read back."));
      return;
    }
    setSaved(undefined);
    if (edit) {
      const current = result.data.entries.find((entry) => entry.rule.id === edit.entry?.rule.id);
      if (edit.entry && !current) {
        setEdit(undefined);
        return;
      }
      setEdit({ entry: current, revision: result.data.revision, epoch: edit.epoch + 1 });
    }
  }
  async function mutate(body: Omit<Mutation, "revision">, revision = state.data?.revision) {
    if (!revision || busy) return;
    setBusy(true);
    setError(undefined);
    try {
      const activation = await api.send<Schema["Activation"]>("device-rules", "PATCH", {
        revision,
        ...body,
      });
      setSaved(activation);
      setEdit(undefined);
      const result = await state.reload();
      if (result.error || !result.data)
        throw (
          result.error ?? new Error("Changes were saved, but device rules could not be read back.")
        );
      setSaved(undefined);
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="mb-5 min-w-0 rounded-lg border border-border bg-background p-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-sm font-medium">Device identification rules</h2>
          <p className="mt-1 max-w-prose text-xs text-muted-foreground">
            Recognise devices from recent DNS queries. Your customisations are kept alongside the
            installed defaults.
          </p>
        </div>
        <Button
          disabled={disabled}
          onClick={() => {
            setError(undefined);
            setEdit({ revision: state.data!.revision, epoch: 0 });
          }}
        >
          Add rule
        </Button>
      </div>
      {error && !edit && (
        <div className="mt-3">
          <ErrorNotice error={error} />
        </div>
      )}
      {(needsReadback || (error && !edit)) && (
        <Button variant="outline" className="mt-3" onClick={() => void reload()} disabled={busy}>
          Reload rules
        </Button>
      )}
      <Resource state={state} retry={() => void reload()}>
        {state.data && (
          <div className="mt-4 space-y-3">
            <ActivationStatus status={needsReadback ? saved! : state.data.status} />
            <Input
              aria-label="Search device rules"
              placeholder="Search names or domains"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
            <ul className="divide-y divide-border">
              {entries.map((entry) => (
                <li key={entry.rule.id} className="flex min-w-0 flex-wrap items-center gap-3 py-3">
                  <DeviceIcon
                    name={entry.rule.icon}
                    className="size-5 shrink-0 text-muted-foreground"
                    aria-hidden="true"
                  />
                  <div className="min-w-0 flex-1 basis-48">
                    <p className="text-sm font-medium wrap-anywhere">{entry.rule.name}</p>
                    <p className="text-xs text-muted-foreground">
                      {entry.origin === "builtin"
                        ? "Built-in"
                        : entry.origin === "modified"
                          ? "Modified"
                          : "Custom"}{" "}
                      ·{" "}
                      {entry.available
                        ? entry.enabled
                          ? "Enabled"
                          : "Disabled"
                        : "Not in this release"}{" "}
                      · {entry.rule.domains.length} domains
                    </p>
                  </div>
                  <div className="flex flex-wrap gap-1">
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={disabled || !entry.available}
                      aria-label={`Edit ${entry.rule.name}`}
                      onClick={() => {
                        setError(undefined);
                        setEdit({ entry, revision: state.data!.revision, epoch: 0 });
                      }}
                    >
                      Edit
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={disabled || !entry.available}
                      aria-label={`${entry.enabled ? "Disable" : "Enable"} ${entry.rule.name}`}
                      onClick={() =>
                        void mutate({
                          action: "enable",
                          id: entry.rule.id,
                          enabled: !entry.enabled,
                        })
                      }
                    >
                      {entry.enabled ? "Disable" : "Enable"}
                    </Button>
                    {entry.origin !== "builtin" && (
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={disabled}
                        aria-label={`${entry.origin === "custom" ? "Delete" : "Reset"} ${entry.rule.name}`}
                        onClick={() => {
                          if (
                            window.confirm(
                              entry.origin === "custom"
                                ? `Delete the custom rule “${entry.rule.name}”?`
                                : `Reset “${entry.rule.name}” to the installed defaults?`,
                            )
                          )
                            void mutate({
                              action: entry.origin === "custom" ? "delete" : "reset",
                              id: entry.rule.id,
                            });
                        }}
                      >
                        {entry.origin === "custom" ? "Delete" : "Reset"}
                      </Button>
                    )}
                  </div>
                </li>
              ))}
            </ul>
            {!entries.length && (
              <p className="text-sm text-muted-foreground">
                {search ? "No rules match this search." : "No device rules configured."}
              </p>
            )}
            <div className="flex flex-wrap items-center justify-between gap-3 border-t border-border pt-3">
              <p className="max-w-prose text-xs text-muted-foreground">
                Changes apply live. DNS evidence refreshes within 30 seconds. Reset uses the
                defaults in the installed version.
              </p>
              <Button
                variant="outline"
                disabled={disabled || !state.data.customized}
                onClick={() => {
                  if (
                    window.confirm(
                      "Reset all device identification rules? This removes all custom rules and restores the installed defaults.",
                    )
                  )
                    void mutate({ action: "reset" });
                }}
              >
                Reset all rules
              </Button>
            </div>
          </div>
        )}
      </Resource>
      {edit && (
        <DeviceRuleEditor
          key={`${edit.entry?.rule.id ?? "new"}:${edit.epoch}`}
          entry={edit.entry}
          busy={busy}
          error={error}
          outdated={!!state.data && state.data.revision !== edit.revision}
          close={() => {
            setEdit(undefined);
            setError(undefined);
          }}
          reload={() => void reload()}
          save={(rule) => void mutate({ action: "save", id: rule.id, rule }, edit.revision)}
        />
      )}
    </section>
  );
}
