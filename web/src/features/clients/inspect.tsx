import { useState } from "react";
import { api } from "@/lib/api";
import { useResource } from "@/lib/hooks";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ErrorNotice } from "@/components/data";
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
      <h3 className="text-sm font-medium">Explain a domain</h3>
      <div className="flex flex-wrap gap-3">
        <label className="min-w-40 flex-1 text-sm">
          Domain
          <Input
            required
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
          {busy ? "Checking…" : "Explain domain"}
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">
        Active policy for the original name; no upstream lookup or alias-chain
        check.
      </p>
      {error && <ErrorNotice error={error} />}
      {result && (
        <div role="status" className="space-y-1 text-sm">
          <p>
            {result.normalized}: <strong>{result.decision.result}</strong> ·{" "}
            {result.handling}
          </p>
          <p className="text-xs text-muted-foreground">
            {result.client_id || "Network defaults"} · {result.matching_method}{" "}
            · generation {result.generation}
          </p>
          {result.decision.rule_id && (
            <p className="break-all text-xs">
              Winning rule: {result.decision.rule_id} ·{" "}
              {sourceLabel(result.decision.scope)} · sources:{" "}
              {result.decision.source_ids.join(", ")}
            </p>
          )}
        </div>
      )}
    </form>
  );
}
