import { describe, it, expect } from "vitest";
import { formatLog } from "./log";

describe("formatLog", () => {
  it("prefixes devpit and skips empty fields", () => {
    expect(formatLog("hydrate failed", { status: 429, hint: null })).toBe(
      "devpit hydrate failed status=429",
    );
  });
});
