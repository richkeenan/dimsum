import { useState } from "react";
import { useResource } from "@/lib/hooks";
import { useQuery } from "@tanstack/react-query";

function resolveAddress(address: string) {
  return api.send<Schema["ClientPolicyExplanation"]>("rules/test", "POST", {
    address,
    name: "identity.invalid",
  });
}
import {
  ownerPolicy,
  selectClass,
  type PolicyRead,
  type Schema,
} from "../clients/model";
import { Ban, ShieldCheck } from "lucide-react";
import { api, normalizeSettings, type Settings } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { ErrorNotice } from "@/components/data";

type RuleResult = {
  action: string;
  settings: Settings;
  notice?: string;
  allowException?: boolean;
};

function isActive(settings: Settings) {
  return (
    !!settings.status &&
    !settings.pending &&
    !settings.error &&
    !settings.status.recovered &&
    !settings.status.restart_required &&
    settings.status.active_revision === settings.status.saved_revision
  );
}

function queryRuleID() {
  return `query-${Array.from(
    crypto.getRandomValues(new Uint8Array(12)),
    (byte) => byte.toString(16).padStart(2, "0"),
  ).join("")}`;
}

function useRuleSave(name: string, address?: string) {
  const [target, setTarget] = useState(address ? "device" : "network");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  const [result, setResult] = useState<RuleResult>();
  async function save(action: string, kind: string) {
    if (busy || result) return;
    setBusy(true);
    setError(undefined);
    try {
      const settings = await api.get<Settings>("settings");
      if (!settings.revision)
        throw new Error(
          "The current settings revision is unavailable. Try again.",
        );
      let clientID: string | undefined;
      let saved: unknown;
      if (target !== "network") {
        if (target === "device") {
          clientID = (await resolveAddress(address!)).client_id;
          if (!clientID)
            throw new Error(
              "This address has no matched configured identity. Configure the device first, or explicitly select network scope.",
            );
        }
        const scope = target === "device" ? "client" : "profile";
        const id = clientID ?? target.slice("profile:".length);
        const current = await api.get<PolicyRead>(
          `client-policy?${new URLSearchParams({ scope, id })}`,
        );
        if (
          scope === "client" &&
          !(current.desired as Schema["PolicyClient"]).id
        )
          throw new Error(
            "Give this legacy device a stable ID in Devices before adding device rules.",
          );
        saved = await api.send("client-policy", "PATCH", {
          revision: current.status.saved_revision,
          scope,
          id,
          fields: [
            {
              path: ["rules"],
              value: [
                ...(ownerPolicy(current).rules ?? []),
                {
                  id: queryRuleID(),
                  kind,
                  action,
                  pattern: name,
                  enabled: true,
                },
              ],
            },
          ],
        });
      } else
        saved = await api.send<unknown>("rules", "POST", {
          revision: settings.revision,
          item: {
            id: queryRuleID(),
            kind,
            action,
            pattern: name,
            enabled: true,
          },
        });
      const result: RuleResult = { action, settings: normalizeSettings(saved) };
      if (isActive(result.settings) && target.startsWith("profile:"))
        result.notice =
          "saved for this profile · inspect an assigned device to check the winning rule";
      if (isActive(result.settings) && !target.startsWith("profile:")) {
        // Activation confirms the configuration was loaded, not that this rule
        // wins. Check the same generation without issuing another DNS lookup.
        try {
          const explanation = await api.send<{
            decision?: { result?: string };
          }>("rules/test", "POST", {
            name,
            ...(clientID ? { client_id: clientID } : {}),
            generation: result.settings.status!.active_generation,
          });
          const decision = explanation.decision?.result;
          if (action === "deny" && decision === "allow") {
            result.notice =
              "saved, but an allow exception still takes precedence.";
            result.allowException = true;
          } else if (decision === "paused") {
            result.notice = "saved · filtering is paused";
          } else if (decision !== (action === "deny" ? "block" : "allow")) {
            result.notice =
              "saved · the current policy does not match this action";
          }
        } catch {
          // The write succeeded. Do not invite a duplicate write if the
          // separate read-only policy check fails or its generation changes.
          result.notice = "saved · current policy could not be checked";
        }
      }
      setResult(result);
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return { busy, error, result, save, target, setTarget };
}

type TargetProps = {
  address?: string;
  value: string;
  change: (value: string) => void;
  disabled: boolean;
};
function RuleTarget(props: TargetProps) {
  return props.address ? <ScopedRuleTarget {...props} /> : null;
}
function ScopedRuleTarget({ address, value, change, disabled }: TargetProps) {
  const profiles = useResource<{ items: Schema["PolicyProfile"][] }>(
    "profiles",
  );
  const resolution = useQuery({
    queryKey: ["api", "client-resolution", address],
    queryFn: () => resolveAddress(address!),
    staleTime: 5000,
    retry: false,
  });
  const id = resolution.data?.client_id;
  return (
    <details className="max-w-60 whitespace-normal text-xs">
      <summary className="cursor-pointer py-2">
        {value === "device"
          ? "This device"
          : value === "network"
            ? "Network defaults"
            : `Profile: ${value.slice(8)}`}
      </summary>
      <label className="text-xs">
        Apply rule to
        <select
          aria-label={`Rule target for ${address}`}
          className={`${selectClass} block max-w-60`}
          value={value}
          disabled={disabled}
          onChange={(e) => change(e.target.value)}
        >
          <option value="device">This device ({address})</option>
          {profiles.data?.items?.map((p) => (
            <option key={p.id} value={`profile:${p.id}`}>
              Profile: {p.name || p.id}
            </option>
          ))}
          <option value="network">Network · all inheriting devices</option>
        </select>
      </label>
      {profiles.error && <ErrorNotice error={profiles.error} />}
      {resolution.error ? (
        <p role="status">
          Device identity unavailable. Retry the action to resolve it again.
        </p>
      ) : resolution.isPending ? (
        <p>Resolving device identity…</p>
      ) : (
        <a
          className="inline-block py-2 text-xs underline"
          href={id ? `/clients?device=${encodeURIComponent(id)}` : "/clients"}
        >
          {id ? "Edit device policy" : "Configure device identity"}
        </a>
      )}
    </details>
  );
}

function RuleSaved({ result }: { result: RuleResult }) {
  const { settings } = result;
  const active = isActive(settings);
  const label = result.action === "deny" ? "Block" : "Allow";
  return (
    <p
      role="status"
      className="max-w-64 text-xs leading-relaxed whitespace-normal wrap-anywhere text-muted-foreground"
    >
      {label} rule{" "}
      {result.notice ??
        (active
          ? "active"
          : settings.error
            ? `saved · activation failed: ${settings.error}`
            : settings.pending
              ? "saved · pending"
              : "saved · not yet active")}
      {result.allowException && (
        <>
          {" "}
          <a className="underline underline-offset-2" href="/rules">
            Edit custom rules
          </a>{" "}
          to remove the allow exception.
        </>
      )}
    </p>
  );
}

export function InlineRuleAction({
  name,
  outcome,
  address,
}: {
  name: string;
  outcome: string;
  address?: string;
}) {
  const state = useRuleSave(name, address);
  if (!name || !["blocked", "forwarded", "cache", "stale"].includes(outcome))
    return null;
  const action = outcome === "blocked" ? "allow" : "deny";
  const label = action === "deny" ? "Block" : "Allow";
  const Icon = action === "deny" ? Ban : ShieldCheck;
  return (
    <div className="space-y-1">
      <RuleTarget
        address={address}
        value={state.target}
        change={state.setTarget}
        disabled={state.busy || !!state.result}
      />
      <Button
        variant="ghost"
        size="sm"
        className="text-[12px]"
        aria-label={`${label} ${name}`}
        title={`${label} this exact domain for the selected scope`}
        disabled={state.busy || !!state.result}
        onClick={() => state.save(action, "exact")}
      >
        <Icon className="size-3.5" strokeWidth={1.5} />
        {state.busy ? "Saving…" : label}
      </Button>
      {state.error && (
        <div className="max-w-60 whitespace-normal">
          <ErrorNotice error={state.error} />
        </div>
      )}
      {state.result &&
        (!isActive(state.result.settings) || state.result.notice) && (
          <RuleSaved result={state.result} />
        )}
    </div>
  );
}

export function QueryRuleForm({
  name,
  outcome,
  address,
}: {
  name: string;
  outcome: string;
  address?: string;
}) {
  const [action, setAction] = useState(
    outcome === "blocked" ? "allow" : "deny",
  );
  const [scope, setScope] = useState("exact");
  const state = useRuleSave(name, address);
  if (outcome === "local")
    return (
      <p className="rounded-md bg-muted p-3 text-sm leading-relaxed text-muted-foreground">
        Local DNS records take priority over domain rules. To change this
        answer, edit its record in{" "}
        <a href="/records" className="underline underline-offset-2">
          Local DNS
        </a>
        .
      </p>
    );
  const selectClass =
    "min-h-9 min-w-0 rounded-md border border-input bg-background px-2.5 py-2 text-sm text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring";
  return (
    <section className="space-y-3 rounded-lg border border-border p-4">
      <h3 className="text-sm font-medium">Create a rule</h3>
      <RuleTarget
        address={address}
        value={state.target}
        change={state.setTarget}
        disabled={state.busy || !!state.result}
      />
      <div className="grid grid-cols-1 gap-3 min-[501px]:grid-cols-2">
        <label className="flex flex-col gap-1.5 text-xs">
          Action
          <select
            className={selectClass}
            value={action}
            disabled={state.busy || !!state.result}
            onChange={(e) => setAction(e.target.value)}
          >
            <option value="deny">Block</option>
            <option value="allow">Allow exception</option>
          </select>
        </label>
        <label className="flex flex-col gap-1.5 text-xs">
          Match scope
          <select
            className={selectClass}
            value={scope}
            disabled={state.busy || !!state.result}
            onChange={(e) => setScope(e.target.value)}
          >
            <option value="exact">Exact name only</option>
            <option value="suffix">Name and descendants</option>
          </select>
        </label>
      </div>
      <p className="text-xs leading-relaxed text-muted-foreground wrap-anywhere">
        {scope === "exact"
          ? `Matches only ${name}. Subdomains are not included.`
          : `Matches ${name} and every descendant, including child.${name}.`}{" "}
        Applies to the selected policy scope.
      </p>
      {state.error && <ErrorNotice error={state.error} />}
      <Button
        disabled={state.busy || !name || !!state.result}
        onClick={() => state.save(action, scope)}
      >
        {state.busy
          ? "Saving…"
          : `Create ${action === "deny" ? "block" : "allow"} rule`}
      </Button>
      {state.result && <RuleSaved result={state.result} />}
    </section>
  );
}
