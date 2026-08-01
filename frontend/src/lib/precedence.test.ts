import { describe, it, expect } from "vitest";
import { STATE_PRECEDENCE } from "./types";
// Raw Go source of truth. The frontend precedence and the Go `precedence` slice
// are hand-kept in sync (ADR-0016); this test fails the build if they drift.
import goSource from "../../../internal/attention/states.go?raw";

describe("state precedence parity with the Go backend", () => {
  it("matches internal/attention/states.go precedence order", () => {
    // Map the Go const names (StateChangesRequested) to their wire values.
    const wireByName = new Map<string, string>();
    for (const m of goSource.matchAll(/\b(State\w+)\s+State\s*=\s*"(\w+)"/g)) {
      wireByName.set(m[1], m[2]);
    }
    expect(wireByName.size).toBe(STATE_PRECEDENCE.length);

    const block = goSource.match(/var precedence = \[\]State\{([\s\S]*?)\}/);
    if (!block)
      throw new Error("could not find the precedence slice in states.go");
    const order = [...block[1].matchAll(/State\w+/g)].map((m) =>
      wireByName.get(m[0]),
    );

    expect(order).toEqual(STATE_PRECEDENCE);
  });
});
