import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api, type Activation } from "@/lib/api";
import { useLive, useResource } from "@/lib/hooks";
import { ErrorNotice, Resource } from "@/components/data";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { ActivationStatus } from "./clients/policy";
import type { components } from "@/lib/openapi";

type BuiltinList = components["schemas"]["BuiltinListRead"];

export function BuiltinListEditor({
  id,
  close,
  onBusy,
}: {
  id: string;
  close: () => void;
  onBusy?: (busy: boolean) => void;
}) {
  const path = `builtin-lists/${encodeURIComponent(id)}`;
  const resource = useResource<BuiltinList>(path);
  const client = useQueryClient();
  const [search, setSearch] = useState("");
  const [domain, setDomain] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  const [activation, setActivation] = useState<Activation>();
  const [confirmReset, setConfirmReset] = useState(false);
  const data = resource.data;
  async function reload() {
    const read = await resource.reload();
    if (read.error) setError(read.error);
    else if (read.data) {
      const next = read.data.status;
      setActivation((previous) =>
        previous &&
        (previous.pending || previous.error) &&
        next.saved_revision !== previous.saved_revision
          ? previous
          : next,
      );
      setError(undefined);
    }
  }
  useLive(!!(activation ?? data?.status)?.pending && !busy, () => void reload());
  async function mutate(action: "add" | "remove" | "restore" | "reset", domain?: string) {
    if (!data || busy) return;
    setBusy(true);
    onBusy?.(true);
    setError(undefined);
    try {
      const result = await api.send<Activation>(path, "PATCH", {
        revision: data.revision,
        action,
        ...(domain ? { domain } : {}),
      });
      setActivation(result);
      setConfirmReset(false);
      if (action === "add") setDomain("");
      const read = await resource.reload();
      if (read.error) throw read.error;
      if (!read.data) throw new Error("Saved list read-back unavailable.");
      // Preserve the mutation's activation failure until a read proves that
      // exact saved revision is active, rather than hiding it with stale data.
      if (read.data.status.active_revision === result.saved_revision && !read.data.status.pending)
        setActivation(read.data.status);
      await Promise.all(
        ["lists", "client-policy", "profiles"].map((key) =>
          client.invalidateQueries({ queryKey: ["api", key] }),
        ),
      );
    } catch (failure) {
      setError(failure as Error);
    } finally {
      setBusy(false);
      onBusy?.(false);
    }
  }
  const shown =
    data?.entries.filter((entry) => entry.domain.includes(search.trim().toLowerCase())) ?? [];
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <p className="text-sm text-muted-foreground">
        Changes affect every profile and device using this list. Domains include their subdomains.
        Removing an exception lets your normal blocking rules decide.
      </p>
      <Resource state={resource} retry={() => void resource.reload()}>
        {data && (
          <>
            <form
              className="flex flex-wrap items-end gap-2"
              onSubmit={(event) => {
                event.preventDefault();
                void mutate("add", domain.trim());
              }}
            >
              <label className="min-w-40 flex-1 space-y-1 text-sm">
                Domain to allow
                <Input
                  value={domain}
                  onChange={(event) => setDomain(event.target.value)}
                  placeholder="analytics.example.com"
                  disabled={busy}
                  autoComplete="off"
                  spellCheck={false}
                />
              </label>
              <Button disabled={busy || !domain.trim() || resource.isFetching} type="submit">
                Add domain
              </Button>
            </form>
            <label className="space-y-1 text-sm">
              Search domains
              <Input
                type="search"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
              />
            </label>
            <p className="text-xs text-muted-foreground" aria-live="polite">
              {data.entries.filter((entry) => !entry.removed).length} allowed domains ·{" "}
              {data.customized ? "Customised" : "Shipped defaults"}
            </p>
            <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain rounded-md border border-border">
              <ul className="divide-y divide-border" aria-label="Allowlist domains">
                {shown.map((entry) => (
                  <li
                    key={entry.domain}
                    className="flex items-center justify-between gap-3 px-3 py-2"
                  >
                    <div className="min-w-0 space-y-1">
                      <p
                        className={`break-all text-sm ${entry.removed ? "text-muted-foreground line-through" : ""}`}
                      >
                        {entry.domain}
                      </p>
                      <Badge variant="secondary">
                        {entry.removed
                          ? "Removed"
                          : entry.origin === "builtin"
                            ? "Shipped"
                            : "Custom"}
                      </Badge>
                    </div>
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={busy || resource.isFetching}
                      aria-label={`${entry.removed ? "Restore" : "Remove"} ${entry.domain}`}
                      onClick={() =>
                        void mutate(entry.removed ? "restore" : "remove", entry.domain)
                      }
                    >
                      {entry.removed ? "Restore" : "Remove"}
                    </Button>
                  </li>
                ))}
              </ul>
              {!shown.length && (
                <p className="p-4 text-sm text-muted-foreground">No matching domains.</p>
              )}
            </div>
          </>
        )}
      </Resource>
      {error && (
        <div className="space-y-2">
          <ErrorNotice error={error} />
          <Button variant="outline" size="sm" disabled={busy} onClick={() => void reload()}>
            Reload list
          </Button>
        </div>
      )}
      {(activation ?? data?.status) && <ActivationStatus status={(activation ?? data?.status)!} />}
      <div className="shrink-0 space-y-3 border-t border-border pt-3">
        {confirmReset && (
          <div className="space-y-2 rounded-md bg-muted p-3">
            <p className="text-sm">
              Remove all additions and exclusions? This restores the defaults shipped with the
              installed version for everyone using this list.
            </p>
            <div className="flex flex-wrap gap-2">
              <Button disabled={busy} onClick={() => void mutate("reset")}>
                Confirm reset
              </Button>
              <Button variant="outline" disabled={busy} onClick={() => setConfirmReset(false)}>
                Cancel reset
              </Button>
            </div>
          </div>
        )}
        <div className="flex flex-wrap items-center justify-between gap-2">
          <Button
            variant="outline"
            disabled={busy || !data?.customized || resource.isFetching}
            onClick={() => setConfirmReset(true)}
          >
            Reset to shipped defaults
          </Button>
          <Button variant="outline" disabled={busy} onClick={close}>
            Done
          </Button>
        </div>
        <p className="text-xs text-muted-foreground">
          Changes save immediately. Your customisations are retained when dimsum updates.
        </p>
      </div>
    </div>
  );
}
