import { useState } from "react";
import { api } from "@/lib/api";
import { useResource } from "@/lib/hooks";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ErrorNotice } from "@/components/data";
import { InfoDetails } from "@/components/info-details";
import { panelClass, selectClass, sourceLabel, type Schema } from "./model";
export function DomainInspector({ clientID = "" }: { clientID?: string }) {
  const clients = useResource<Schema["ClientsResponse"]>("clients");
  const [client, setClient] = useState(clientID);
  const [name, setName] = useState("");
  const [result, setResult] = useState<Schema["ClientPolicyExplanation"]>();
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  return (
    <form
      className={panelClass}
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setResult(undefined);
        setError(undefined);
        try {
          setResult(
            await api.send("rules/test", "POST", {
              name,
              ...(client ? { client_id: client } : {}),
            }),
          );
        } catch (e) {
          setError(e as Error);
        } finally {
          setBusy(false);
        }
      }}
    >
      <div className="space-y-1">
        <h3 className="text-sm font-medium">Check domain access</h3>
        <p className="text-xs text-muted-foreground">
          See whether your current rules block or allow a domain for a device, and which rule
          applies.
        </p>
      </div>
      <div className="flex flex-wrap gap-3">
        <label className="min-w-40 flex-1 text-sm">
          Domain
          <Input
            required
            disabled={busy}
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              setResult(undefined);
            }}
            placeholder="ads.example.com"
          />
        </label>
        <label className="min-w-0 max-w-full text-sm">
          Device
          <select
            className={`${selectClass} block max-w-full`}
            aria-label="Device"
            value={client}
            disabled={busy}
            onChange={(e) => {
              setClient(e.target.value);
              setResult(undefined);
            }}
          >
            <option value="">Network defaults</option>
            {clients.data?.items?.map((c) => (
              <option key={c.policy_id} value={c.policy_id}>
                {c.name || c.policy_id}
              </option>
            ))}
          </select>
        </label>
        <Button className="self-end" disabled={busy}>
          {busy ? "Checking…" : "Check domain"}
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">
        Checks your current rules only. This does not test whether the website is reachable.
      </p>
      {error && <ErrorNotice error={error} />}
      {result && (
        <div className="rounded-md bg-muted px-4 py-3">
          <div className="flex items-start justify-between gap-2">
            <div role="status" className="space-y-1 text-sm">
              <p className="font-medium">{domainOutcome(result)}</p>
              <p className="text-xs text-muted-foreground">
                {result.normalized} ·{" "}
                {result.client_id
                  ? clients.data?.items?.find((c) => c.policy_id === result.client_id)?.name ||
                    result.client_id
                  : "Network defaults"}
              </p>
              <p className="text-xs text-muted-foreground">{domainReason(result)}</p>
            </div>
            <InfoDetails label="Domain check details">
              <p>
                Checks the name you entered using the active configuration. It does not query
                upstream DNS or check other names in a DNS alias chain.
              </p>
              <p>
                DNS decision: {result.decision.result} · Handling: {result.handling}
              </p>
              <p>Configuration generation: {result.generation}</p>
              {result.decision.rule_id && (
                <p>
                  Matched rule: {result.decision.rule_id} · {sourceLabel(result.decision.scope)}
                </p>
              )}
              {result.decision.source_ids.length > 0 && (
                <p>
                  Matching sources: {result.decision.source_ids.join(", ")}. These may include rules
                  that did not determine the result.
                </p>
              )}
              {result.effective.profile_id && <p>Profile: {result.effective.profile_id}</p>}
            </InfoDetails>
          </div>
        </div>
      )}
    </form>
  );
}

function domainOutcome(result: Schema["ClientPolicyExplanation"]) {
  if (result.handling === "private_reverse") return "Kept within your network";
  if (result.decision.result === "paused" && !result.effective.blocking.value)
    return "Filtering is turned off";
  return {
    forward: "Not blocked by current rules",
    allow: "Allowed by a matching rule",
    block: "Blocked by current rules",
    local: "Answered by local DNS",
    paused: "Filtering is paused",
  }[result.decision.result];
}

function domainReason(result: Schema["ClientPolicyExplanation"]) {
  if (result.handling === "private_reverse")
    return "This is a private-address lookup. Dimsum does not send it to external DNS servers.";
  switch (result.decision.result) {
    case "forward":
      return "No matching rule blocks this domain.";
    case "local":
      return "Dimsum handles this name locally instead of asking an external DNS server.";
    case "paused":
      return "Blocking rules are not being applied to this device right now.";
    default: {
      const owner =
        result.decision.scope.kind === "client"
          ? "this device’s rules"
          : result.decision.scope.kind === "profile"
            ? "this device’s profile"
            : "network defaults";
      return `Matched a rule in ${owner}.`;
    }
  }
}
