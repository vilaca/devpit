import { describe, it, expect, vi, afterEach } from "vitest";
import { isRetriable, parseRetryAfter, retryDelay, retryHintMs } from "./retry";
import { ApiRequestError } from "./api";

describe("retryDelay", () => {
  it("backs off exponentially, capped at 30s", () => {
    expect(retryDelay(0, null)).toBe(1000);
    expect(retryDelay(1, null)).toBe(2000);
    expect(retryDelay(2, null)).toBe(4000);
    expect(retryDelay(10, null)).toBe(30_000);
  });

  it("uses the exponential floor when the hint is shorter", () => {
    expect(retryDelay(0, 200)).toBe(1000);
  });

  it("prefers a longer Retry-After hint, capped at 60s", () => {
    expect(retryDelay(0, 5_000)).toBe(5_000);
    expect(retryDelay(0, 120_000)).toBe(60_000);
  });
});

describe("isRetriable", () => {
  it("retries 429, 5xx, and fetch TypeError", () => {
    expect(isRetriable(new ApiRequestError(429, "unknown", "limited"))).toBe(
      true,
    );
    expect(isRetriable(new ApiRequestError(500, "internal", "boom"))).toBe(
      true,
    );
    expect(
      isRetriable(new ApiRequestError(503, "unknown", "unavailable")),
    ).toBe(true);
    expect(isRetriable(new TypeError("Failed to fetch"))).toBe(true);
  });

  it("does not retry 4xx other than 429, or aborts", () => {
    expect(isRetriable(new ApiRequestError(400, "bad_request", "nope"))).toBe(
      false,
    );
    expect(isRetriable(new ApiRequestError(404, "not_found", "gone"))).toBe(
      false,
    );
    expect(isRetriable(new DOMException("aborted", "AbortError"))).toBe(false);
    expect(isRetriable(new Error("programmer mistake"))).toBe(false);
  });
});

describe("parseRetryAfter", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("reads delta-seconds from Retry-After", () => {
    const h = new Headers({ "Retry-After": "12" });
    expect(parseRetryAfter(h)).toBe(12_000);
  });

  it("reads an HTTP-date Retry-After", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-27T00:00:00Z"));
    const h = new Headers({
      "Retry-After": "Thu, 27 Aug 2026 00:00:08 GMT",
    });
    expect(parseRetryAfter(h)).toBe(8_000);
  });

  it("takes the larger of Retry-After and X-RateLimit-Reset", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-27T00:00:00Z"));
    const h = new Headers({
      "Retry-After": "2",
      "X-RateLimit-Reset": String(
        Math.floor(new Date("2026-08-27T00:00:10Z").getTime() / 1000),
      ),
    });
    expect(parseRetryAfter(h)).toBe(10_000);
  });

  it("returns null when no hint is present", () => {
    expect(parseRetryAfter(new Headers())).toBeNull();
  });
});

describe("retryHintMs", () => {
  it("reads retryAfterMs from ApiRequestError", () => {
    expect(
      retryHintMs(new ApiRequestError(429, "unknown", "limited", 4_500)),
    ).toBe(4_500);
    expect(retryHintMs(new TypeError("Failed to fetch"))).toBeNull();
  });
});
