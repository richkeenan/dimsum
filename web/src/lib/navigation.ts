export const pages = [
  "overview",
  "performance",
  "queries",
  "clients",
  "lists",
  "rules",
  "records",
  "upstreams",
  "dhcp",
  "settings",
  "jobs",
  "diagnostics",
];
export const filterKeys = [
  "name",
  "client",
  "outcome",
  "qtype",
  "source_id",
  "rule_id",
  "upstream_id",
  "boot_id",
  "generation",
] as const;
export type ViewSearch = {
  range?: string;
  from?: string;
  to?: string;
} & Partial<Record<(typeof filterKeys)[number], string>>;
// DNS identifiers are decimal strings, including values beyond JS integer precision.
export function parseViewSearch(search: string): Record<string, string> {
  return Object.fromEntries(new URLSearchParams(search));
}
export function stringifyViewSearch(search: Record<string, unknown>): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(search)) {
    if (typeof value === "string" && value !== "") params.set(key, value);
  }
  const encoded = params.toString();
  return encoded ? `?${encoded}` : "";
}
export function validateView(raw: Record<string, unknown>): ViewSearch {
  const result: ViewSearch = {};
  if (
    typeof raw.range === "string" &&
    ["1h", "24h", "7d", "custom"].includes(raw.range)
  )
    result.range = raw.range;
  for (const key of [...filterKeys, "from", "to"] as const)
    if (typeof raw[key] === "string" && raw[key].length <= 512)
      result[key] = raw[key];
  if (result.range === "custom") {
    const from = Date.parse(result.from ?? ""),
      to = Date.parse(result.to ?? "");
    if (
      !Number.isFinite(from) ||
      !Number.isFinite(to) ||
      from >= to ||
      to - from > 366 * 86400000
    ) {
      delete result.from;
      delete result.to;
      result.range = "24h";
    }
  }
  return result;
}
