// Thin typed wrapper over the DevPit REST API. Same-origin: in production the
// devpit binary serves both the SPA and the API from :7474; in dev Vite proxies
// the API paths through (vite.config.ts). So all paths here are root-relative.

import type {
  AttentionResponse,
  ConnectionsResponse,
  SyncLogResponse,
  ApiError,
} from "./types";

// ApiRequestError carries the uniform error envelope (docs/REST_API.md) so
// callers can distinguish not_found / bad_request / internal.
export class ApiRequestError extends Error {
  constructor(
    readonly status: number,
    readonly code: ApiError["error"] | "unknown",
    message: string,
  ) {
    super(message);
    this.name = "ApiRequestError";
  }
}

async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(path, { headers: { Accept: "application/json" }, signal });
  if (!res.ok) {
    let code: ApiError["error"] | "unknown" = "unknown";
    let message = res.statusText;
    try {
      const body = (await res.json()) as ApiError;
      code = body.error ?? "unknown";
      message = body.message ?? message;
    } catch {
      // non-JSON error body; keep the status text
    }
    throw new ApiRequestError(res.status, code, message);
  }
  return (await res.json()) as T;
}

export function getAttention(state?: string, signal?: AbortSignal): Promise<AttentionResponse> {
  const q = state ? `?state=${encodeURIComponent(state)}` : "";
  return getJSON<AttentionResponse>(`/attention${q}`, signal);
}

export function getConnections(signal?: AbortSignal): Promise<ConnectionsResponse> {
  return getJSON<ConnectionsResponse>("/connections", signal);
}

export function getSyncLog(connectionId?: string, signal?: AbortSignal): Promise<SyncLogResponse> {
  const q = connectionId ? `?connection=${encodeURIComponent(connectionId)}` : "";
  return getJSON<SyncLogResponse>(`/sync-log${q}`, signal);
}

// setFlag / clearFlag drive the "Handle next" pinned zone. Read-only model
// otherwise (ADR-0017): the flag is the only client-originated state. Both
// return 204 No Content.
async function flag(id: string, method: "PUT" | "DELETE"): Promise<void> {
  const res = await fetch(`/items/${encodeURIComponent(id)}/flag`, { method });
  if (!res.ok && res.status !== 204) {
    throw new ApiRequestError(res.status, "unknown", `failed to ${method} flag`);
  }
}

export const setFlag = (id: string): Promise<void> => flag(id, "PUT");
export const clearFlag = (id: string): Promise<void> => flag(id, "DELETE");
