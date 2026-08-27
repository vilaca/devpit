// REST retry policy for dashboard hydrates. SSE reconnect is separate
// (sse.ts) and does not interpret HTTP status — EventSource never surfaces
// 429 / Retry-After.
//
// Retriable: fetch network failures (TypeError), 429, 500, 502, 503, 504.
// Delay: max(1s × 2^attempt capped at 30s, Retry-After / X-RateLimit-Reset
// hint capped at 60s). 429 always retries; the hint wins when it is longer
// than the exponential step.

export const retryExpCapMs = 30_000;
export const retryHintCapMs = 60_000;

const retriableStatus = new Set([429, 500, 502, 503, 504]);

export function isRetriable(err: unknown): boolean {
  if (err instanceof DOMException && err.name === "AbortError") return false;
  const status = httpStatus(err);
  if (status !== null) return retriableStatus.has(status);
  // fetch() network failures are TypeError.
  return err instanceof TypeError;
}

export function retryHintMs(err: unknown): number | null {
  if (typeof err !== "object" || err === null || !("retryAfterMs" in err)) {
    return null;
  }
  const v = err.retryAfterMs;
  return typeof v === "number" && v > 0 ? v : null;
}

export function retryDelay(attempt: number, hintMs: number | null): number {
  const exp = Math.min(retryExpCapMs, 1000 * 2 ** Math.max(0, attempt));
  if (hintMs == null || hintMs <= 0) return exp;
  return Math.max(exp, Math.min(hintMs, retryHintCapMs));
}

// parseRetryAfter reads Retry-After (delta-seconds or HTTP-date) and
// X-RateLimit-Reset (Unix seconds), returning the larger hint in ms.
export function parseRetryAfter(headers: Headers): number | null {
  let hint = 0;
  const ra = headers.get("Retry-After");
  if (ra) {
    const secs = Number(ra);
    if (Number.isFinite(secs) && secs > 0) {
      hint = secs * 1000;
    } else {
      const when = Date.parse(ra);
      if (!Number.isNaN(when)) hint = Math.max(0, when - Date.now());
    }
  }
  const reset =
    headers.get("X-RateLimit-Reset") ?? headers.get("X-Ratelimit-Reset");
  if (reset) {
    const unix = Number(reset);
    if (Number.isFinite(unix) && unix > 0) {
      const until = unix * 1000 - Date.now();
      if (until > hint) hint = until;
    }
  }
  return hint > 0 ? hint : null;
}

function httpStatus(err: unknown): number | null {
  if (typeof err !== "object" || err === null || !("status" in err)) {
    return null;
  }
  const status = err.status;
  return typeof status === "number" ? status : null;
}
