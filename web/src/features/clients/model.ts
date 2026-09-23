import type { components, paths } from "@/lib/openapi";
export type Schema = components["schemas"];
export type PolicyRead = Schema["ClientPolicyRead"];
export type PolicyMutation = Schema["ClientPolicyMutation"];
export type PolicyScope = PolicyMutation["scope"];
export type Preview =
  paths["/api/v1/client-policy/preview"]["post"]["responses"][200]["content"]["application/json"];
export type Observation = Schema["ObservedClients"]["items"][number];
export type ClientRow = {
  key: string;
  configured?: Schema["PolicyClient"];
  observed: Observation[];
};
export function clientIdentity(row: ClientRow) {
  const observation = row.observed[0];
  return {
    name: row.configured?.name || observation?.name || row.configured?.id,
    address:
      observation?.address ||
      row.configured?.address ||
      row.configured?.selectors?.addresses?.[0] ||
      "",
    device: observation?.device,
    source: observation?.name_source,
  };
}
export function clientLastSeen(row: ClientRow) {
  const times = row.observed.map((o) => Date.parse(o.last_seen || "")).filter(Number.isFinite);
  return times.length ? new Date(Math.max(...times)).toISOString() : undefined;
}
export function clientCount(row: ClientRow, key: "count" | "blocked") {
  // Use the constructor: oxc-transform-react 0.145.0 lowers inline 0n to undefined.
  return row.observed.reduce((sum, o) => sum + BigInt(o[key] ?? 0), BigInt(0));
}
export function mergeClients(data: Partial<Schema["ClientsResponse"]>): ClientRow[] {
  const result: ClientRow[] = (data.items ?? []).map((configured) => ({
    key: configured.policy_id!,
    configured,
    observed: [],
  }));
  for (const observed of data.observed?.items ?? []) {
    const owner = observed.client_id && result.find((row) => row.key === observed.client_id);
    if (owner) owner.observed.push(observed);
    else
      result.push({
        key: `observed:${observed.address}`,
        observed: [observed],
      });
  }
  return result;
}
export function sourceLabel(source: Schema["PolicyScope"]) {
  return source.kind
    ? `${source.kind === "client" ? "Device" : "Profile"}: ${source.id}`
    : "Network default";
}
export function ownerPolicy(read: PolicyRead): Schema["PolicyOverrides"] {
  if (read.scope === "client") return (read.desired as Schema["PolicyClient"]).overrides ?? {};
  if (read.scope === "profile") return (read.desired as Schema["PolicyProfile"]).policy ?? {};
  return read.desired as Schema["NetworkPolicyDesired"];
}
export const selectClass =
  "min-h-10 min-w-0 rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring";
export const panelClass =
  "min-w-0 space-y-4 wrap-anywhere rounded-lg border border-border bg-background p-4 sm:p-5";
export function words(value: string) {
  return value.split(/[\s,]+/).filter(Boolean);
}
export function policyRuleID() {
  return policyID("rule");
}
export function policyID(prefix: string) {
  return `${prefix}-${Array.from(crypto.getRandomValues(new Uint8Array(12)), (byte) => byte.toString(16).padStart(2, "0")).join("")}`;
}
