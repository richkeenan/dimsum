// Transport boundary: keep contract adaptation here, not inside visual components.
import type { components, paths } from "./openapi";
export type Activation = components["schemas"]["Activation"];
export type Mutation = components["schemas"]["Mutation"];
export type Job = components["schemas"]["Job"];
type LoginResult =
  paths["/session"]["post"]["responses"][200]["content"]["application/json"];
let csrfToken = sessionStorage.getItem("dimsum-csrf") ?? "";
export type Row = Record<string, unknown>;
export interface Meta {
  from?: string;
  to?: string;
  updated_at?: string;
  complete?: boolean;
  incomplete?: boolean;
  gaps?: unknown[];
}
export interface Summary extends Meta {
  total?: string;
  blocked?: string;
  cached?: string;
  stale?: string;
  errors?: string;
  active_clients?: string;
  dns?: string;
  blocking?: boolean;
  list_health?: string;
  storage_health?: string;
  [key: string]: unknown;
}
export interface Page extends Meta {
  items: Row[];
  next_cursor?: string;
}
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
  send: <T>(path: string, method: string, body?: unknown) =>
    request<T>("/api/v1/" + path, {
      method,
      body: body === undefined ? undefined : JSON.stringify(body),
    }),
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
      : undefined;
  return {
    ...raw,
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
export function count(value: unknown): string {
  const s = text(value);
  return /^\d+$/.test(s) ? BigInt(s).toLocaleString() : s;
}
export function percentage(part: unknown, total: unknown): string {
  if (
    !/^\d+$/.test(String(part)) ||
    !/^\d+$/.test(String(total)) ||
    BigInt(String(total)) === 0n
  )
    return "—";
  return (
    (
      Number((BigInt(String(part)) * 1000n) / BigInt(String(total))) / 10
    ).toFixed(1) + "%"
  );
}
