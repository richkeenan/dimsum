// Transport boundary: keep contract adaptation here, not inside visual components.
import type { components, paths } from "./openapi";
export type Activation = components["schemas"]["Activation"];
export type Mutation = components["schemas"]["Mutation"];
export type Job = components["schemas"]["Job"];
export type DHCPSettings = components["schemas"]["DHCPSettings"];
export type DHCPReservation = components["schemas"]["DHCPReservation"];
export type DHCPStatusResponse =
  paths["/api/v1/dhcp/status"]["get"]["responses"][200]["content"]["application/json"];
export type DHCPConfigResponse =
  paths["/api/v1/dhcp"]["get"]["responses"][200]["content"]["application/json"];
export type DHCPLeasesResponse =
  paths["/api/v1/dhcp/leases"]["get"]["responses"][200]["content"]["application/json"];
export type DHCPReservationsResponse =
  paths["/api/v1/dhcp/reservations"]["get"]["responses"][200]["content"]["application/json"];
type LoginResult =
  paths["/session"]["post"]["responses"][200]["content"]["application/json"];
let csrfToken =
  typeof sessionStorage === "undefined"
    ? ""
    : (sessionStorage.getItem("dimsum-csrf") ?? "");
export type Row = Record<string, unknown>;
export interface Meta {
  range?: { from: string; to: string };
  from?: string;
  to?: string;
  updated_at?: string;
  complete?: boolean;
  incomplete?: boolean;
  gaps?: unknown[];
}
export type Summary = components["schemas"]["HistorySummary"];
export type Page = components["schemas"]["HistoryQueries"];
export type QueryDetail = components["schemas"]["HistoryDetail"];
export type Series = components["schemas"]["HistorySeries"];
export type Point = components["schemas"]["HistoryPoint"];
export type Performance = components["schemas"]["HistoryPerformance"];
export type Latency = components["schemas"]["HistoryLatency"];
export type LatencyPoint = components["schemas"]["HistoryLatencyPoint"];
export type LatencyBand = components["schemas"]["HistoryLatencyBand"];
export type Rankings = components["schemas"]["HistoryRankings"];
export type ClientsResponse = components["schemas"]["ClientsResponse"];
export type Device = components["schemas"]["DeviceEnrichment"];
export const outcomes = [
  "local",
  "blocked",
  "cache",
  "stale",
  "forwarded",
  "error",
  "rejected",
] as const;
export interface Settings {
  revision: string;
  active_generation?: string;
  pending?: boolean;
  error?: string;
  status?: Activation;
  config?: Row;
  source?: string;
  [key: string]: unknown;
}
export interface Edit {
  path: string[];
  value: unknown;
}
export class APIError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public fields?: unknown,
    public requestID?: string,
  ) {
    super(message);
  }
}
export async function request<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  let response: Response;
  try {
    response = await fetch(path, {
      credentials: "same-origin",
      ...options,
      headers: {
        Accept: "application/json",
        ...(options.body ? { "Content-Type": "application/json" } : {}),
        ...(options.method && options.method !== "GET" && csrfToken
          ? { "X-CSRF-Token": csrfToken }
          : {}),
        ...options.headers,
      },
    });
  } catch (error) {
    if (options.signal?.aborted) throw error;
    throw new APIError(
      0,
      "offline",
      "Cannot reach the administration service. Check the connection and retry.",
    );
  }
  const text = await response.text();
  let body: unknown;
  try {
    body = text ? JSON.parse(text) : undefined;
  } catch {
    throw new APIError(
      response.status,
      "invalid_response",
      "The service returned an unreadable response.",
    );
  }
  if (!response.ok) {
    const raw = body && typeof body === "object" ? (body as Row) : {};
    const detail =
      raw.error && typeof raw.error === "object" ? (raw.error as Row) : raw;
    if (response.status === 401) {
      csrfToken = "";
      sessionStorage.removeItem("dimsum-csrf");
      window.dispatchEvent(new Event("session-expired"));
    }
    throw new APIError(
      response.status,
      String(detail.code ?? "request_failed"),
      String(detail.message ?? `Request failed (${response.status}).`),
      detail.fields ?? detail.field_errors,
      String(detail.request_id ?? ""),
    );
  }
  return body as T;
}
export const api = {
  get: async <T>(path: string, signal?: AbortSignal): Promise<T> => {
    const result = await request<unknown>("/api/v1/" + path, { signal });
    return (path === "settings" ? normalizeSettings(result) : result) as T;
  },
  send: async <T>(path: string, method: string, body?: unknown) => {
    const result = await request<T>("/api/v1/" + path, {
      method,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (
      path === "dhcp" ||
      path.startsWith("dhcp/reservations") ||
      [
        "settings",
        "lists",
        "rules",
        "records",
        "clients",
        "client-policy",
        "profiles",
        "upstreams",
        "blocking",
      ].includes(path)
    )
      window.dispatchEvent(new Event("configuration-changed"));
    return result;
  },
  login: async (password: string) => {
    const result = await request<LoginResult>("/session", {
      method: "POST",
      body: JSON.stringify({ password }),
    });
    csrfToken = result?.csrf_token ?? "";
    if (csrfToken) sessionStorage.setItem("dimsum-csrf", csrfToken);
    return result;
  },
  logout: async () => {
    await request("/session", { method: "DELETE" });
    csrfToken = "";
    sessionStorage.removeItem("dimsum-csrf");
  },
  edit: (revision: string, edits: Edit[]) =>
    api.send<Settings>("settings", "PATCH", { revision, edits }),
};
export function normalizeSettings(value: unknown): Settings {
  const raw = value && typeof value === "object" ? (value as Row) : {};
  const status =
    raw.status && typeof raw.status === "object"
      ? (raw.status as Activation)
      : typeof raw.saved_revision === "string" &&
          typeof raw.active_revision === "string"
        ? (raw as Activation)
        : undefined;
  return {
    ...raw,
    status,
    revision: status?.saved_revision ?? String(raw.revision ?? ""),
    active_generation:
      status?.active_generation ??
      (raw.active_generation as string | undefined),
    pending: status?.pending ?? (raw.pending as boolean | undefined),
    error: String(raw.configuration_error ?? status?.error ?? raw.error ?? ""),
  } as Settings;
}
export function collectionRows(value: unknown): Row[] {
  const raw =
    value && typeof value === "object" ? (value as Row).items : undefined;
  return Array.isArray(raw)
    ? raw.map((item, index) => ({
        ...(typeof item === "object" && item !== null
          ? item
          : { address: item }),
        __index: index,
      }))
    : [];
}
export function rows(value: unknown, key = "items"): Row[] {
  const list = Array.isArray(value)
    ? value
    : value && typeof value === "object"
      ? (value as Row)[key]
      : undefined;
  return Array.isArray(list)
    ? list.filter(
        (r): r is Row => !!r && typeof r === "object" && !Array.isArray(r),
      )
    : [];
}
export function text(value: unknown): string {
  return value === null || value === undefined
    ? "—"
    : typeof value === "object"
      ? JSON.stringify(value)
      : String(value);
}
export function count(value: unknown, locale?: string): string {
  const s = text(value);
  return /^\d+$/.test(s) ? BigInt(s).toLocaleString(locale) : s;
}
export function percentage(
  part: unknown,
  total: unknown,
  locale?: string,
): string {
  if (
    !/^\d+$/.test(String(part)) ||
    !/^\d+$/.test(String(total)) ||
    BigInt(String(total)) === 0n
  )
    return "—";
  const ratio =
    Number((BigInt(String(part)) * 1000n) / BigInt(String(total))) / 1000;
  return ratio.toLocaleString(locale, {
    style: "percent",
    minimumFractionDigits: 1,
    maximumFractionDigits: 1,
  });
}
// Decimal-unit formatting never passes an identifier or counter through Number.
export function microsecondsToMS(value: unknown, locale?: string): string {
  if (typeof value !== "string" || !/^\d+$/.test(value)) return "—";
  const n = BigInt(value);
  const fraction = (n % 1000n).toLocaleString(locale, {
    minimumIntegerDigits: 3,
    useGrouping: false,
  });
  return new Intl.NumberFormat(locale, {
    minimumFractionDigits: 3,
    maximumFractionDigits: 3,
  })
    .formatToParts(n / 1000n)
    .map((part) => (part.type === "fraction" ? fraction : part.value))
    .join("");
}
export function queryParameters(
  filters: Record<string, string>,
  cursor?: string,
): URLSearchParams {
  const p = new URLSearchParams({ limit: "100" });
  for (const key of [
    "name",
    "client",
    "outcome",
    "qtype",
    "boot_id",
    "generation",
    "rule_id",
    "source_id",
    "upstream_id",
  ])
    if (filters[key]?.trim()) p.set(key, filters[key].trim());
  if (cursor) p.set("cursor", cursor);
  return p;
}
export function historyWindow(
  preset: string,
  now: number,
  custom?: { from: string; to: string },
) {
  const duration =
    (
      { "1h": 3600000, "24h": 86400000, "7d": 604800000 } as Record<
        string,
        number
      >
    )[preset] ?? 86400000;
  let width = duration <= 3600000 ? 60000 : 3600000;
  let to = now,
    from = to - duration;
  if (preset === "custom" && custom) {
    from = Date.parse(custom.from);
    to = Date.parse(custom.to);
    if (
      !Number.isFinite(from) ||
      !Number.isFinite(to) ||
      to <= from ||
      to - from > 366 * 86400000
    )
      throw new Error("Invalid history window.");
    width = 60000;
    while (Math.ceil(to / width) - Math.floor(from / width) > 1500)
      width = width === 60000 ? 3600000 : 86400000;
  }
  return {
    params: new URLSearchParams({
      from: new Date(from).toISOString(),
      to: new Date(to).toISOString(),
    }).toString(),
    resolution: width / 1000,
  };
}
export const maxArchiveBytes = 2 * 1024 * 1024;
export async function archiveBase64(file: File): Promise<string> {
  if (file.size > maxArchiveBytes)
    throw new Error("Choose an archive no larger than 2 MiB.");
  if (!file.size) throw new Error("The archive is empty.");
  const bytes = new Uint8Array(await file.arrayBuffer());
  let binary = "";
  for (let i = 0; i < bytes.length; i += 8192)
    binary += String.fromCharCode(...bytes.subarray(i, i + 8192));
  return btoa(binary);
}
export function backupURL(result: unknown): string | undefined {
  const url =
    result && typeof result === "object"
      ? (result as Row).download_url
      : undefined;
  return typeof url === "string" &&
    /^\/api\/v1\/config\/backups\/[a-f0-9]{32}$/.test(url)
    ? url
    : undefined;
}
