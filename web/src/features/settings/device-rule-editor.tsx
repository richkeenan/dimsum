import { useEffect, useState } from "react";
import type { components } from "@/lib/openapi";
import { DeviceIconField, isDeviceIcon } from "@/components/device-icon";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ErrorNotice } from "@/components/data";
import { Dialog, DialogContent, DialogTitle, DialogDescription } from "@/components/ui/dialog";
import { selectClass } from "@/features/clients/model";

type Rule = components["schemas"]["DeviceRule"];
type Entry = components["schemas"]["DeviceRuleEntry"];
const categories: NonNullable<Rule["category"]>[] = [
  "unknown",
  "phone",
  "tablet",
  "laptop",
  "desktop",
  "tv",
  "speaker",
  "printer",
  "camera",
  "lighting",
  "appliance",
  "server",
  "console",
];

export function DeviceRuleEditor({
  entry,
  busy,
  error,
  outdated,
  save,
  close,
  reload,
}: {
  entry?: Entry;
  busy: boolean;
  error?: Error;
  outdated: boolean;
  save: (rule: Rule) => void;
  close: () => void;
  reload: () => void;
}) {
  const initial = entry?.rule ?? { id: "", name: "", domains: [] };
  const [rule, setRule] = useState<Rule>(initial);
  const [domains, setDomains] = useState(initial.domains.join("\n"));
  const dirty =
    JSON.stringify(rule) !== JSON.stringify(initial) || domains !== initial.domains.join("\n");
  const validIcon = !rule.icon || isDeviceIcon(rule.icon);
  const confirmLeave = () => !dirty || window.confirm("Discard unsaved device rule changes?");
  useEffect(() => {
    if (!dirty) return;
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault();
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy && confirmLeave()) close();
      }}
    >
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-xl">
        <DialogTitle>{entry ? `Edit ${entry.rule.name}` : "Add device rule"}</DialogTitle>
        <DialogDescription>
          Identify a device family from exact DNS names. Use device-specific services rather than
          websites or shared cloud hosts.
        </DialogDescription>
        <form
          className="min-w-0 space-y-4"
          onSubmit={(event) => {
            event.preventDefault();
            if (validIcon) save({ ...rule, domains: domains.split(/\s+/).filter(Boolean) });
          }}
        >
          <fieldset disabled={busy} className="min-w-0 space-y-4">
            <div className="grid gap-3 sm:grid-cols-2">
              <label className="space-y-1 text-sm">
                Rule ID
                <Input
                  required
                  disabled={!!entry}
                  value={rule.id}
                  maxLength={64}
                  pattern={"[a-z0-9_\\-]+"}
                  placeholder="example-washer"
                  onChange={(e) => setRule({ ...rule, id: e.target.value })}
                />
              </label>
              <label className="space-y-1 text-sm">
                Rule name
                <Input
                  required
                  value={rule.name}
                  maxLength={256}
                  placeholder="Smart washing machine"
                  onChange={(e) => setRule({ ...rule, name: e.target.value })}
                />
              </label>
              <label className="space-y-1 text-sm">
                Category
                <select
                  aria-label="Category"
                  className={`${selectClass} w-full`}
                  value={rule.category ?? "unknown"}
                  onChange={(e) =>
                    setRule({ ...rule, category: e.target.value as Rule["category"] })
                  }
                >
                  {categories.map((c) => (
                    <option key={c} value={c}>
                      {c === "unknown"
                        ? "Generic device"
                        : c === "tv"
                          ? "TV"
                          : c[0].toUpperCase() + c.slice(1)}
                    </option>
                  ))}
                </select>
              </label>
              <DeviceIconField
                value={rule.icon ?? ""}
                onChange={(icon) => setRule({ ...rule, icon })}
              />
            </div>
            <label className="block space-y-1 text-sm">
              DNS domains
              <textarea
                aria-label="DNS domains"
                className="min-h-32 w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-sm"
                required={!entry?.builtin}
                value={domains}
                onChange={(e) => setDomains(e.target.value)}
                spellCheck={false}
              />
              <span className="block text-xs text-muted-foreground">
                One exact lowercase hostname per line. No wildcards or trailing dots.
              </span>
            </label>
            <label className="block space-y-1 text-sm">
              Evidence explanation (optional)
              <Input
                value={rule.reason ?? ""}
                maxLength={1024}
                placeholder="Use the default explanation"
                onChange={(e) => setRule({ ...rule, reason: e.target.value })}
              />
            </label>
            {entry?.builtin && (
              <details className="text-xs text-muted-foreground">
                <summary className="min-h-10 cursor-pointer py-2">Installed defaults</summary>
                <p className="mb-2">{entry.builtin.name}</p>
                <ul className="space-y-1 font-mono wrap-anywhere">
                  {entry.builtin.domains.map((domain) => (
                    <li key={domain}>{domain}</li>
                  ))}
                </ul>
              </details>
            )}
            {outdated && (
              <p className="text-sm text-muted-foreground">
                Configuration changed since this editor opened. Reload to discard this draft and use
                the latest revision.
              </p>
            )}
            {error && <ErrorNotice error={error} />}
            <div className="flex flex-wrap justify-end gap-2">
              {(outdated || error) && (
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => {
                    if (confirmLeave()) reload();
                  }}
                >
                  Reload rule
                </Button>
              )}
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  if (confirmLeave()) close();
                }}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={!validIcon || outdated}>
                Save rule
              </Button>
            </div>
          </fieldset>
        </form>
      </DialogContent>
    </Dialog>
  );
}
