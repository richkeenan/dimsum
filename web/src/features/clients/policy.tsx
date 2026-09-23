import { useCallback, useEffect, useState } from "react";
import { api, APIError, type Activation } from "@/lib/api";
import { useResource } from "@/lib/hooks";
import { ErrorNotice, Resource } from "@/components/data";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  ownerPolicy,
  policyRuleID,
  policyID,
  panelClass,
  selectClass,
  sourceLabel,
  words,
  type PolicyRead,
  type PolicyMutation,
  type PolicyScope,
  type Preview,
  type Schema,
} from "./model";

export function ActivationStatus({ status }: { status: Activation }) {
  const active =
    !status.pending &&
    !status.error &&
    !status.recovered &&
    !status.restart_required &&
    status.saved_revision === status.active_revision;
  if (
    active &&
    !status.sources.some((s) => s.enabled && (!s.usable || s.error))
  )
    return null;
  return (
    <div
      role={status.error ? "alert" : "status"}
      className={`space-y-1 text-xs wrap-anywhere ${
        active
          ? "text-muted-foreground"
          : `rounded-md border border-l-[3px] bg-muted px-3 py-2 text-foreground ${status.error ? "border-destructive" : "border-border border-l-primary"}`
      }`}
    >
      {!active && (
        <p>
          {status.error
            ? `Saved · activation failed: ${status.error}`
            : status.restart_required
              ? "Saved · restart required"
              : "Applying changes…"}
        </p>
      )}
      {status.sources
        .filter((s) => s.enabled && (!s.usable || s.error))
        .map((s) => (
          <p key={s.id} className="text-destructive">
            {s.id}:{" "}
            {s.usable
              ? "Using last download; refresh failed"
              : "Source not active"}
            {s.error ? ` — ${s.error}` : ""}
          </p>
        ))}
    </div>
  );
}

export function PolicyEditor({
  scope,
  id,
  onDeleted,
  onPromoted,
  onDirty,
}: {
  scope: PolicyScope;
  id?: string;
  onDeleted?: () => void;
  onPromoted?: (id: string) => void;
  onDirty?: (dirty: boolean) => void;
}) {
  const state = useResource<PolicyRead>(
    `client-policy?${new URLSearchParams({ scope, ...(id ? { id } : {}) })}`,
  );
  const [snapshot, setSnapshot] = useState<PolicyRead>();
  const [epoch, setEpoch] = useState(0);
  const [dirty, setDirty] = useState(false);
  const reportDirty = useCallback(
    (value: boolean) => {
      setDirty(value);
      onDirty?.(value);
    },
    [onDirty],
  );
  useEffect(() => () => onDirty?.(false), [onDirty]);
  useEffect(() => {
    if (
      state.data &&
      !state.error &&
      !state.isPlaceholderData &&
      !state.isFetching &&
      (!snapshot ||
        snapshot.scope !== scope ||
        snapshot.id !== (id ?? "") ||
        (!dirty && snapshot !== state.data))
    )
      setSnapshot(state.data);
  }, [
    state.data,
    state.error,
    state.isPlaceholderData,
    state.isFetching,
    snapshot,
    scope,
    id,
    dirty,
  ]);
  return (
    <Resource state={state} retry={() => void state.reload()}>
      {snapshot && snapshot.scope === scope && snapshot.id === (id ?? "") && (
        <PolicyForm
          key={`${scope}:${id}:${snapshot.status.saved_revision}:${epoch}`}
          read={snapshot}
          liveStatus={state.data?.status}
          onDirty={reportDirty}
          reload={async () => {
            const result = await state.reload();
            if (result.error || !result.data)
              throw (
                result.error ?? new Error("Saved policy read-back unavailable.")
              );
            if (result.data) {
              setSnapshot(result.data);
              setEpoch((x) => x + 1);
            }
          }}
          onDeleted={onDeleted}
          onPromoted={onPromoted}
        />
      )}
    </Resource>
  );
}

function PolicyForm({
  read,
  liveStatus,
  onDirty,
  reload,
  onDeleted,
  onPromoted,
}: {
  read: PolicyRead;
  liveStatus?: Activation;
  onDirty: (dirty: boolean) => void;
  reload: () => Promise<void>;
  onDeleted?: () => void;
  onPromoted?: (id: string) => void;
}) {
  const profiles = useResource<{ items: Schema["PolicyProfile"][] }>(
    "profiles",
  );
  const catalog = useResource<Schema["Catalog"]>("catalog");
  const lists = useResource<{ items: Schema["PolicySubscription"][] }>("lists");
  const availableLists = catalog.data?.items.map((item) => ({
    ...item,
    id:
      lists.data?.items.find(
        (list) =>
          list.url === item.url &&
          list.dialect === item.dialect &&
          list.domain_kind === item.domain_kind,
      )?.id ?? item.id,
  }));
  const clients = useResource<Schema["ClientsResponse"]>("clients");
  const own = ownerPolicy(read);
  const [fields, setFields] = useState<Schema["PolicyField"][]>([]);
  const [extra, setExtra] = useState<Partial<PolicyMutation>>({});
  const [subscriptions, setSubscriptions] = useState<
    Schema["PolicySubscription"][]
  >([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  const [status, setStatus] = useState<Activation>();
  const [preview, setPreview] = useState<Preview>();
  const [previewError, setPreviewError] = useState<Error>();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const desiredClient = read.desired as Schema["PolicyClient"];
  const [name, setName] = useState(desiredClient.name ?? "");
  const [promotionID] = useState(() => policyID("device"));
  const [addresses, setAddresses] = useState(
    (
      desiredClient.selectors?.addresses ??
      (desiredClient.address ? [desiredClient.address] : [])
    ).join("\n"),
  );
  const [macs, setMacs] = useState(
    desiredClient.selectors?.macs?.join("\n") ?? "",
  );
  const [cidrs, setCidrs] = useState(
    desiredClient.selectors?.cidrs?.join("\n") ?? "",
  );
  const [lease, setLease] = useState("");
  const [primary, setPrimary] = useState(
    (own.upstream ?? read.effective.upstream).upstreams.join("\n"),
  );
  const [fallback, setFallback] = useState(
    (own.upstream ?? read.effective.upstream).fallback_upstreams?.join("\n") ??
      "",
  );
  const [rulePattern, setRulePattern] = useState("");
  const [ruleKind, setRuleKind] =
    useState<Schema["PolicyRule"]["kind"]>("exact");
  const [ruleAction, setRuleAction] =
    useState<Schema["PolicyRule"]["action"]>("deny");
  const keyOf = (path: string[]) => JSON.stringify(path);
  const field = (path: string[]) =>
    fields.find((f) => keyOf(f.path) === keyOf(path));
  const setField = (
    path: string[],
    value: Schema["PolicyField"]["value"] | undefined,
  ) => {
    setExtra((e) => {
      const next = { ...e };
      delete next.reset_all;
      return next;
    });
    const saved =
      path[0] === "lists"
        ? own.lists?.[path[1]]
        : own[path[0] as keyof typeof own];
    const resetFields: Schema["PolicyField"][] = extra.reset_all
      ? [
          ...(own.blocking !== undefined
            ? [{ path: ["blocking"], reset: true as const }]
            : []),
          ...Object.keys(own.lists ?? {}).map((id) => ({
            path: ["lists", id],
            reset: true as const,
          })),
          ...(own.upstream
            ? [{ path: ["upstream"], reset: true as const }]
            : []),
          ...(own.rules ? [{ path: ["rules"], reset: true as const }] : []),
        ]
      : [];
    setFields((old) => [
      ...(extra.reset_all ? resetFields : old).filter(
        (f) => keyOf(f.path) !== keyOf(path),
      ),
      ...(JSON.stringify(saved) === JSON.stringify(value)
        ? []
        : [
            value === undefined
              ? { path, reset: true as const }
              : { path, value },
          ]),
    ]);
    if (path[0] === "lists" && value !== true)
      setSubscriptions((old) => old.filter((s) => s.id !== path[1]));
    if (path[0] === "lists" && value === true) {
      const list = lists.data?.items.find(
        (s) => s.id === path[1] && !s.enabled,
      );
      if (list)
        setSubscriptions((old) => [
          ...old.filter((s) => s.id !== list.id),
          {
            id: list.id,
            url: list.url,
            dialect: list.dialect,
            domain_kind: list.domain_kind,
            enabled: true,
          },
        ]);
    }
  };
  const changed =
    fields.length +
    Object.keys(extra).filter((k) => k !== "promote_id").length +
    subscriptions.filter(
      (s) => !fields.some((f) => f.path[0] === "lists" && f.path[1] === s.id),
    ).length;
  const unstaged =
    rulePattern.trim() !== "" ||
    addresses !==
      (
        desiredClient.selectors?.addresses ??
        (desiredClient.address ? [desiredClient.address] : [])
      ).join("\n") ||
    macs !== (desiredClient.selectors?.macs ?? []).join("\n") ||
    cidrs !== (desiredClient.selectors?.cidrs ?? []).join("\n");
  useEffect(
    () => onDirty(!status && (changed > 0 || unstaged)),
    [changed, unstaged, status, onDirty],
  );
  const mutation: PolicyMutation = {
    revision: read.status.saved_revision,
    scope: read.scope,
    ...(read.scope !== "network" ? { id: read.id } : {}),
    ...extra,
    ...(fields.length ? { fields } : {}),
    ...(subscriptions.length ? { subscribe: subscriptions } : {}),
    ...(read.scope === "client" && !desiredClient.id
      ? { promote_id: promotionID }
      : {}),
  };
  const serialized = JSON.stringify(mutation);
  useEffect(() => {
    setPreview(undefined);
    setPreviewError(undefined);
    if (!changed) return;
    let cancelled = false;
    const timer = setTimeout(() => {
      api
        .send<Preview>("client-policy/preview", "POST", JSON.parse(serialized))
        .then((value) => {
          if (!cancelled) setPreview(value);
        })
        .catch((e) => {
          if (!cancelled) setPreviewError(e);
        });
    }, 250);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [serialized, changed]);
  async function save(body = mutation) {
    if (status || busy) return;
    if (!body.delete && body.name !== undefined && !body.name.trim()) {
      setError(new Error("Enter a name before saving."));
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      const result = await api.send<Activation>("client-policy", "PATCH", body);
      setStatus(result);
      onDirty(false);
      if (body.delete) onDeleted?.();
      else if (body.promote_id && onPromoted) onPromoted(body.promote_id);
      else await reload();
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  const effective = preview?.effective ?? read.effective;
  const routeEdit = field(["upstream"]);
  useEffect(() => {
    if (!changed && !own.upstream) {
      setPrimary(read.effective.upstream.upstreams.join("\n"));
      setFallback(read.effective.upstream.fallback_upstreams?.join("\n") ?? "");
      return;
    }
    if (
      preview?.effective &&
      (routeEdit?.reset || extra.reset_all || (!own.upstream && !routeEdit))
    ) {
      setPrimary(preview.effective.upstream.upstreams.join("\n"));
      setFallback(
        preview.effective.upstream.fallback_upstreams?.join("\n") ?? "",
      );
    }
  }, [
    preview,
    routeEdit,
    own.upstream,
    extra.reset_all,
    changed,
    read.effective.upstream,
  ]);
  const boolRow = (
    label: string,
    path: string[],
    saved: boolean | undefined,
    value: Schema["EffectivePolicyBool"],
    health?: string,
  ) => {
    const edit = field(path);
    const selected = edit
      ? edit.reset
        ? "inherit"
        : edit.value
          ? "on"
          : "off"
      : extra.reset_all
        ? "inherit"
        : saved === undefined
          ? "inherit"
          : saved
            ? "on"
            : "off";
    return (
      <div
        key={keyOf(path)}
        className="grid gap-2 border-b border-border py-3 sm:grid-cols-[minmax(0,1fr)_14rem] sm:items-center"
      >
        <div className="min-w-0 wrap-anywhere">
          <label htmlFor={keyOf(path)} className="text-sm font-medium">
            {label}
          </label>
          <p className="text-xs text-muted-foreground">
            {read.scope !== "network"
              ? `${value.value ? "On" : "Off"} — ${value.source.kind === "client" ? "Device setting" : value.source.kind === "profile" ? `Profile: ${profiles.data?.items.find((p) => p.id === value.source.id)?.name || value.source.id}` : "Network default"}`
              : ""}
            {health ? `${read.scope !== "network" ? " · " : ""}${health}` : ""}
          </p>
          {path[0] === "lists" &&
            lists.data?.items.some((s) => s.id === path[1] && !s.enabled) &&
            !subscriptions.some((s) => s.id === path[1]) && (
              <Button
                variant="link"
                size="sm"
                className="px-0"
                onClick={() => setField(path, true)}
              >
                Activate list
              </Button>
            )}
        </div>
        <select
          id={keyOf(path)}
          className={selectClass}
          disabled={path[0] === "lists" && !lists.data}
          value={
            read.scope === "network" && selected === "inherit"
              ? value.value
                ? "on"
                : "off"
              : selected
          }
          onChange={(e) =>
            setField(
              path,
              e.target.value === "inherit"
                ? undefined
                : e.target.value === "on",
            )
          }
        >
          {read.scope !== "network" && (
            <option value="inherit">
              {read.scope === "client" &&
              (extra.profile ?? desiredClient.profile)
                ? "Use profile setting"
                : "Use network default"}
            </option>
          )}
          <option value="on">On</option>
          <option value="off">Off</option>
        </select>
      </div>
    );
  };
  const rulesEdit = field(["rules"]);
  const ownRules = rulesEdit
    ? rulesEdit.reset
      ? []
      : (rulesEdit.value as Schema["PolicyRule"][])
    : extra.reset_all
      ? []
      : (own.rules ?? []);
  const scopeName =
    read.scope === "network"
      ? "Network defaults"
      : `${read.scope === "client" ? "Device" : "Profile"}: ${desiredClient.name || read.id}`;
  function discard() {
    setFields([]);
    setExtra({});
    setSubscriptions([]);
    setError(undefined);
    setPreview(undefined);
    setName(desiredClient.name ?? "");
    setPrimary((own.upstream ?? read.effective.upstream).upstreams.join("\n"));
    setFallback(
      (own.upstream ?? read.effective.upstream).fallback_upstreams?.join(
        "\n",
      ) ?? "",
    );
    setAddresses(
      (
        desiredClient.selectors?.addresses ??
        (desiredClient.address ? [desiredClient.address] : [])
      ).join("\n"),
    );
    setMacs(desiredClient.selectors?.macs?.join("\n") ?? "");
    setCidrs(desiredClient.selectors?.cidrs?.join("\n") ?? "");
    setLease("");
    setRulePattern("");
  }
  return (
    <div className="space-y-4">
      <fieldset
        disabled={busy || !!status}
        className="flex min-w-0 flex-col gap-4"
      >
        <section className={panelClass}>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <h2 className="text-base font-medium">{scopeName}</h2>
          </div>
          <p className="text-xs text-muted-foreground">
            {read.scope === "network"
              ? "Used by devices unless their profile or device settings override them."
              : read.scope === "profile"
                ? "Applies to devices assigned to this profile. Other settings follow network defaults."
                : "Choose a profile or customize settings for this device."}
          </p>
          <ActivationStatus status={status ?? liveStatus ?? read.status} />
          {changed > 0 && (
            <p className="text-xs font-medium text-primary" role="status">
              Unsaved changes · save to apply.
            </p>
          )}
          {!changed &&
            read.active &&
            (liveStatus ?? read.status).saved_revision !==
              (liveStatus ?? read.status).active_revision && (
              <p className="text-xs">
                Showing saved settings. The active policy differs.
              </p>
            )}
          {liveStatus &&
            liveStatus.saved_revision !== read.status.saved_revision && (
              <p className="text-xs text-muted-foreground">
                Newer settings are available. Your draft still uses the revision
                opened here.
              </p>
            )}
          {!read.active && (
            <p className="text-xs text-muted-foreground">
              These settings are not active yet.
            </p>
          )}
          {read.scope !== "network" && (
            <div className="grid gap-3 sm:grid-cols-2">
              {
                <label className="space-y-1 text-sm">
                  Name
                  <Input
                    value={name}
                    onChange={(e) => {
                      setName(e.target.value);
                      setExtra((x) => {
                        const next = { ...x };
                        if (e.target.value === (desiredClient.name ?? ""))
                          delete next.name;
                        else next.name = e.target.value;
                        return next;
                      });
                    }}
                  />
                </label>
              }
              {read.scope === "client" && (
                <label className="space-y-1 text-sm">
                  Profile
                  <select
                    className={`${selectClass} w-full`}
                    aria-label="Profile"
                    value={extra.profile ?? desiredClient.profile ?? ""}
                    onChange={(e) =>
                      setExtra((x) => {
                        const next = { ...x };
                        if (e.target.value === (desiredClient.profile ?? ""))
                          delete next.profile;
                        else next.profile = e.target.value;
                        return next;
                      })
                    }
                  >
                    <option value="">No profile · network defaults</option>
                    {profiles.data?.items.map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.name || p.id}
                      </option>
                    ))}
                  </select>
                </label>
              )}
            </div>
          )}
          {profiles.error && <ErrorNotice error={profiles.error} />}
          {boolRow(
            "DNS filtering",
            ["blocking"],
            own.blocking,
            effective?.blocking ?? read.effective.blocking,
          )}
          <p className="text-xs text-muted-foreground">
            When on, DNS filtering blocks domains using the filter lists and
            custom rules below.
          </p>
          {
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h3 className="text-sm font-medium">Filter lists</h3>
              {read.scope !== "network" && (
                <Button
                  variant="ghost"
                  disabled={
                    !!extra.reset_all || !Object.keys(own.lists ?? {}).length
                  }
                  onClick={() => {
                    setSubscriptions([]);
                    setFields((old) => [
                      ...old.filter((f) => f.path[0] !== "lists"),
                      ...Object.keys(own.lists ?? {}).map((id) => ({
                        path: ["lists", id],
                        reset: true as const,
                      })),
                    ]);
                  }}
                >
                  Use defaults for all lists
                </Button>
              )}
            </div>
          }
          <p className="text-xs text-muted-foreground">
            Choose which lists of domains to block.
          </p>
          {Object.entries({
            ...Object.fromEntries(
              subscriptions.map((s) => [
                s.id,
                {
                  value: true,
                  source:
                    read.scope === "network"
                      ? {}
                      : { kind: read.scope, id: read.id },
                },
              ]),
            ),
            ...(effective?.lists ?? read.effective.lists),
          }).map(([id, value]) => {
            const source = read.status.sources.find((s) => s.id === id);
            return boolRow(
              availableLists?.find((c) => c.id === id)?.label ?? id,
              ["lists", id],
              own.lists?.[id],
              value,
              subscriptions.some((s) => s.id === id)
                ? "Downloads on save"
                : !source?.enabled
                  ? "Not downloaded"
                  : !source.usable
                    ? "Source not active"
                    : source.error
                      ? "Last download retained; refresh failed"
                      : `${source.rules.toLocaleString()} loaded rules`,
            );
          })}
          {
            <details>
              <summary className="min-h-10 cursor-pointer py-2 text-sm focus-visible:outline-2 focus-visible:outline-ring">
                Add an available list
              </summary>
              <div className="space-y-2 pt-2">
                {catalog.error && <ErrorNotice error={catalog.error} />}
                {catalog.loading && (
                  <p role="status" className="text-xs text-muted-foreground">
                    Loading available lists…
                  </p>
                )}
                {availableLists &&
                  !availableLists.some(
                    (c) => !(c.id in read.effective.lists),
                  ) && (
                    <p className="text-xs text-muted-foreground">
                      All available lists are already shown above.
                    </p>
                  )}
                {availableLists
                  ?.filter((c) => !(c.id in read.effective.lists))
                  .map((c) => (
                    <div
                      key={c.id}
                      className="flex flex-wrap items-center justify-between gap-2"
                    >
                      <div className="min-w-0 flex-1 wrap-anywhere text-sm">
                        {c.label}
                        {!c.available && (
                          <p className="text-xs text-muted-foreground">
                            {c.unavailable_reason}
                          </p>
                        )}
                      </div>
                      <Button
                        variant="outline"
                        size="sm"
                        aria-label={`Add ${c.label}`}
                        className="min-h-10"
                        disabled={
                          !c.available ||
                          subscriptions.some((s) => s.id === c.id)
                        }
                        onClick={() => {
                          setSubscriptions((old) => [
                            ...old,
                            {
                              id: c.id,
                              url: c.url,
                              dialect: c.dialect,
                              domain_kind: c.domain_kind,
                              enabled: true,
                            },
                          ]);
                          setField(["lists", c.id], true);
                        }}
                      >
                        Add
                      </Button>
                    </div>
                  ))}
              </div>
            </details>
          }
          {subscriptions.length > 0 && (
            <p className="text-xs text-muted-foreground">
              New lists download on save. Applied only to this{" "}
              {read.scope === "client" ? "device" : read.scope}; existing
              network application is retained.
            </p>
          )}
        </section>
        {
          <section className={panelClass}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h3 className="text-sm font-medium">Custom rules</h3>
              {extra.reset_all && own.rules && (
                <p className="text-xs">
                  Removing owner rules:{" "}
                  {own.rules.map((r) => r.pattern).join(", ") ||
                    "empty explicit collection"}
                </p>
              )}
              {read.scope !== "network" && (
                <Button
                  variant="ghost"
                  onClick={() => setField(["rules"], undefined)}
                >
                  Reset rules
                </Button>
              )}
            </div>
            <p className="text-xs text-muted-foreground">
              {(effective?.rules ?? []).filter((r) => !r.source.kind).length}{" "}
              network rules apply automatically.{" "}
              <a href="/rules" className="underline underline-offset-4">
                View network rules
              </a>
            </p>
            {read.scope === "client" && desiredClient.profile && (
              <p className="text-xs text-muted-foreground">
                Rules from the assigned profile also apply.{" "}
                <a href="/profiles" className="underline underline-offset-4">
                  View profiles
                </a>
              </p>
            )}
            {read.scope !== "network" && (
              <>
                {ownRules.map((rule) => (
                  <div
                    key={rule.id}
                    className="flex flex-wrap items-center justify-between gap-2 text-sm"
                  >
                    <span className="min-w-0 flex-1 basis-48 wrap-anywhere">
                      {rule.action === "allow" ? "Allow" : "Block"}{" "}
                      {rule.pattern} ({rule.kind})
                      {!rule.enabled ? " · disabled" : ""}
                    </span>
                    <div className="flex flex-wrap gap-2">
                      <Button
                        size="sm"
                        variant="ghost"
                        className="min-h-10"
                        aria-label={`${rule.enabled ? "Disable" : "Enable"} ${rule.pattern}`}
                        onClick={() =>
                          setField(
                            ["rules"],
                            ownRules.map((r) =>
                              r.id === rule.id
                                ? { ...r, enabled: !r.enabled }
                                : r,
                            ),
                          )
                        }
                      >
                        {rule.enabled ? "Disable" : "Enable"}
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="min-h-10"
                        aria-label={`Remove ${rule.pattern}`}
                        onClick={() =>
                          setField(
                            ["rules"],
                            ownRules.filter((r) => r.id !== rule.id),
                          )
                        }
                      >
                        Remove
                      </Button>
                    </div>
                  </div>
                ))}
                <div className="flex flex-wrap gap-2">
                  <Input
                    aria-label="Rule domain or pattern"
                    className="min-w-40 flex-1"
                    value={rulePattern}
                    onChange={(e) => setRulePattern(e.target.value)}
                    placeholder="ads.example.com"
                  />
                  <select
                    aria-label="Rule action"
                    className={selectClass}
                    value={ruleAction}
                    onChange={(e) =>
                      setRuleAction(e.target.value as typeof ruleAction)
                    }
                  >
                    <option value="deny">Block</option>
                    <option value="allow">Allow</option>
                  </select>
                  <select
                    aria-label="Rule match"
                    className={selectClass}
                    value={ruleKind}
                    onChange={(e) =>
                      setRuleKind(e.target.value as typeof ruleKind)
                    }
                  >
                    {["exact", "suffix", "wildcard", "glob", "regex"].map(
                      (k) => (
                        <option key={k}>{k}</option>
                      ),
                    )}
                  </select>
                  <Button
                    variant="outline"
                    disabled={!rulePattern.trim()}
                    onClick={() => {
                      setField(
                        ["rules"],
                        [
                          ...ownRules,
                          {
                            id: policyRuleID(),
                            kind: ruleKind,
                            action: ruleAction,
                            pattern: rulePattern.trim(),
                            enabled: true,
                          },
                        ],
                      );
                      setRulePattern("");
                    }}
                  >
                    Add rule
                  </Button>
                </div>
              </>
            )}
          </section>
        }
        {read.scope === "client" && (
          <section className={panelClass}>
            <h3 className="text-sm font-medium">Device pause</h3>
            <p className="text-xs text-muted-foreground">
              {effective?.global_paused
                ? "Global pause is active"
                : effective?.filtering
                  ? "Filtering enabled (list availability shown above)"
                  : "Filtering is off or paused"}
              {desiredClient.paused_until
                ? ` · device pause until ${new Date(desiredClient.paused_until).toLocaleString()}`
                : ""}
            </p>
            {extra.paused_until && (
              <p role="status" className="text-sm font-medium text-primary">
                Pending pause until{" "}
                {new Date(extra.paused_until).toLocaleString()} · applies on
                save.
              </p>
            )}
            {extra.reset_pause && (
              <p role="status" className="text-sm font-medium text-primary">
                Pending: resume device on save.
              </p>
            )}
            <div className="flex flex-wrap gap-2">
              {[5, 30, 60].map((minutes) => (
                <Button
                  key={minutes}
                  variant="outline"
                  onClick={() =>
                    setExtra((x) => {
                      const n = {
                        ...x,
                        paused_until: new Date(
                          Date.now() + minutes * 60000,
                        ).toISOString(),
                      };
                      delete n.reset_pause;
                      return n;
                    })
                  }
                >
                  Pause {minutes} min
                </Button>
              ))}
              <Button
                variant="ghost"
                onClick={() =>
                  setExtra((x) => {
                    const n = { ...x, reset_pause: true };
                    delete n.paused_until;
                    return n;
                  })
                }
              >
                Resume device
              </Button>
            </div>
          </section>
        )}
        {read.scope === "client" &&
          (extra.selectors || extra.lease_address) && (
            <section
              className={panelClass}
              aria-label="Pending matching identity"
            >
              <h3 className="text-sm font-medium">Pending matching identity</h3>
              <div className="space-y-1 wrap-anywhere text-sm" role="status">
                {extra.lease_address ? (
                  <p>Use the lease MAC for {extra.lease_address}.</p>
                ) : (
                  <>
                    <p>
                      Addresses:{" "}
                      {extra.selectors?.addresses?.join(", ") || "None"}
                    </p>
                    <p>
                      MAC addresses:{" "}
                      {extra.selectors?.macs?.join(", ") || "None"}
                    </p>
                    <p>
                      Networks: {extra.selectors?.cidrs?.join(", ") || "None"}
                    </p>
                  </>
                )}
                <p className="text-xs text-muted-foreground">
                  Replaces matching selectors on save; profile and policy are
                  retained.
                </p>
              </div>
            </section>
          )}
        {read.scope === "client" && (
          <section className={panelClass}>
            <details>
              <summary className="min-h-10 cursor-pointer content-center wrap-anywhere text-sm font-medium focus-visible:outline-2 focus-visible:outline-ring">
                Matching identity · {read.id}
              </summary>
              <p className="my-3 text-xs text-muted-foreground">
                Relinking replaces all selectors and preserves this ID, profile
                and policy. Names are labels, not matching identities.
              </p>
              <div className="grid gap-3 sm:grid-cols-3">
                {[
                  ["Addresses", addresses, setAddresses],
                  ["MAC addresses", macs, setMacs],
                  ["Networks (CIDR)", cidrs, setCidrs],
                ].map(([label, value, setter]) => (
                  <label key={String(label)} className="space-y-1 text-sm">
                    {String(label)}
                    <textarea
                      aria-label={String(label)}
                      className={`${selectClass} w-full`}
                      value={String(value)}
                      onChange={(e) =>
                        (setter as (s: string) => void)(e.target.value)
                      }
                    />
                  </label>
                ))}
              </div>
              <Button
                className="mt-3"
                variant="outline"
                onClick={() =>
                  setExtra((x) => {
                    const n = {
                      ...x,
                      selectors: {
                        addresses: words(addresses),
                        macs: words(macs),
                        cidrs: words(cidrs),
                      },
                    };
                    delete n.lease_address;
                    return n;
                  })
                }
              >
                Stage selector replacement
              </Button>
              <label className="mt-4 block space-y-1 text-sm">
                Relink to authoritative DHCP lease
                <select
                  className={`${selectClass} w-full`}
                  aria-label="Relink to authoritative DHCP lease"
                  value={lease}
                  onChange={(e) => setLease(e.target.value)}
                >
                  <option value="">Select an active lease</option>
                  {clients.data?.observed?.items
                    .filter((o) => o.authoritative_mac)
                    .map((o) => (
                      <option key={o.address} value={o.address}>
                        {o.name || o.address} · {o.address} ·{" "}
                        {o.authoritative_mac}
                      </option>
                    ))}
                </select>
              </label>
              <Button
                className="mt-2"
                variant="outline"
                disabled={!lease}
                onClick={() =>
                  setExtra((x) => {
                    const n = { ...x, lease_address: lease };
                    delete n.selectors;
                    return n;
                  })
                }
              >
                Use lease MAC only
              </Button>
            </details>
          </section>
        )}
        <details className={panelClass}>
          <summary className="cursor-pointer text-sm font-medium">
            Advanced: upstream servers
          </summary>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h3 className="text-sm font-medium">Upstream servers</h3>
            <Button
              variant="ghost"
              onClick={() => setField(["upstream"], undefined)}
            >
              Reset upstream
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">
            {(effective?.upstream.upstreams ?? []).join(", ") ||
              "No configured upstream"}{" "}
            · {sourceLabel(effective?.upstream_source ?? {})}
          </p>
          {extra.reset_all && own.upstream && (
            <p className="text-xs">
              Using inherited upstream servers after saving.
            </p>
          )}
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="space-y-1 text-sm">
              Primary servers
              <textarea
                aria-label="Primary servers"
                className={`${selectClass} w-full`}
                value={primary}
                onChange={(e) => {
                  setPrimary(e.target.value);
                  setField(["upstream"], {
                    upstreams: words(e.target.value),
                    fallback_upstreams: words(fallback),
                  });
                }}
              />
            </label>
            <label className="space-y-1 text-sm">
              Fallback servers
              <textarea
                aria-label="Fallback servers"
                className={`${selectClass} w-full`}
                value={fallback}
                onChange={(e) => {
                  setFallback(e.target.value);
                  setField(["upstream"], {
                    upstreams: words(primary),
                    fallback_upstreams: words(e.target.value),
                  });
                }}
              />
            </label>
          </div>
        </details>
        <section
          className={`space-y-2 rounded-lg border border-border bg-background p-3 sm:p-4 ${changed ? "sticky bottom-2 z-10 shadow-sm" : ""}`}
        >
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="min-w-0 wrap-anywhere text-sm">
              <strong>
                {changed} {changed === 1 ? "change" : "changes"}
              </strong>{" "}
              · {scopeName}
              {preview && (
                <p className="mt-1 max-h-16 overflow-y-auto wrap-anywhere text-xs text-muted-foreground">
                  {preview.changed_clients.length} affected devices
                  {preview.network_changed ? " · network changed" : ""}
                  {preview.changed_clients.length
                    ? `: ${preview.changed_clients.join(", ")}`
                    : ""}
                  {preview.downloads_pending.length
                    ? ` · downloads pending: ${preview.downloads_pending.join(", ")}`
                    : ""}
                </p>
              )}
            </div>
            <div className="flex flex-wrap items-center gap-2">
              {(changed > 0 || unstaged) && (
                <Button
                  variant="ghost"
                  aria-label="Discard staged changes"
                  onClick={discard}
                >
                  Discard
                </Button>
              )}
              <Button disabled={!changed || busy} onClick={() => void save()}>
                {busy
                  ? "Saving…"
                  : `Save ${changed} ${changed === 1 ? "change" : "changes"}`}
              </Button>
            </div>
          </div>
          {previewError && <ErrorNotice error={previewError} />}
          {error && <ErrorNotice error={error} />}
          {((error instanceof APIError && error.status === 409) ||
            (liveStatus &&
              liveStatus.saved_revision !== read.status.saved_revision)) && (
            <Button
              variant="outline"
              onClick={() => void reload().catch((e: Error) => setError(e))}
            >
              Reload saved policy
            </Button>
          )}
        </section>
        {read.scope !== "network" && (
          <Button
            variant="ghost"
            disabled={busy}
            className="self-start"
            onClick={() => {
              setFields([]);
              setSubscriptions([]);
              setExtra({
                reset_all: true,
                ...(read.scope === "client" ? { reset_pause: true } : {}),
              });
            }}
          >
            Reset all overrides
          </Button>
        )}
        {read.scope !== "network" && onDeleted && (
          <div className="flex flex-wrap items-center gap-3">
            <Button
              variant="ghost"
              onClick={() => setConfirmDelete(!confirmDelete)}
            >
              Delete {read.scope === "client" ? "device" : "profile"}
            </Button>
            {confirmDelete && (
              <>
                <span className="text-sm">
                  Remove this configuration? References must be resolved first.
                </span>
                <Button
                  variant="destructive"
                  disabled={busy}
                  onClick={() =>
                    void save({
                      revision: read.status.saved_revision,
                      scope: read.scope,
                      id: read.id,
                      delete: true,
                    })
                  }
                >
                  Confirm delete
                </Button>
              </>
            )}
          </div>
        )}
      </fieldset>
      {status && error && (
        <div>
          <p role="status" className="text-sm">
            Changes were saved. Reading the saved policy failed; the activation
            result above is retained.
          </p>
          <Button
            variant="outline"
            onClick={async () => {
              setBusy(true);
              try {
                await reload();
              } catch (e) {
                setError(e as Error);
              } finally {
                setBusy(false);
              }
            }}
            disabled={busy}
          >
            Retry saved policy read
          </Button>
        </div>
      )}
    </div>
  );
}
