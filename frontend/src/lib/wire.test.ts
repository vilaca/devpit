import { describe, it, expect } from "vitest";
// Raw Go source of truth. The frontend AttentionItem interface and the Go
// attentionItem struct are hand-kept in sync; this test fails the build if
// they drift.
import goSource from "../../../internal/api/attention.go?raw";
import tsSource from "./types.ts?raw";

describe("wire-shape parity: attentionItem (Go) vs AttentionItem (TypeScript)", () => {
  it("Go and TypeScript field sets are identical", () => {
    // Extract JSON tag names from the Go attentionItem struct. The struct
    // starts with "type attentionItem struct {" and ends at the matching "}".
    const structBlock = goSource.match(
      /type attentionItem struct \{([\s\S]*?)\n\}/,
    );
    if (!structBlock)
      throw new Error("could not find attentionItem struct in attention.go");

    const goFields = new Set<string>();
    for (const m of structBlock[1].matchAll(/`json:"([^",]+)/g)) {
      goFields.add(m[1]);
    }
    expect(goFields.size).toBeGreaterThan(0);

    // Extract field names from the TypeScript AttentionItem interface. The
    // interface body starts after "export interface AttentionItem {" and ends
    // at the matching "}".
    const ifaceBlock = tsSource.match(
      /export interface AttentionItem \{([\s\S]*?)\n\}/,
    );
    if (!ifaceBlock)
      throw new Error("could not find AttentionItem interface in types.ts");

    const tsFields = new Set<string>();
    // Match "fieldName?" or "fieldName:" — the name before the optional "?"
    // or ":". Skip comment-only lines.
    for (const m of ifaceBlock[1].matchAll(/^\s+(\w+)\??\s*[:?]/gm)) {
      tsFields.add(m[1]);
    }
    expect(tsFields.size).toBeGreaterThan(0);

    // The sets must be identical.
    const onlyInGo = [...goFields].filter((f) => !tsFields.has(f));
    const onlyInTS = [...tsFields].filter((f) => !goFields.has(f));

    expect(
      onlyInGo,
      "fields in Go attentionItem but not in TS AttentionItem",
    ).toEqual([]);
    expect(
      onlyInTS,
      "fields in TS AttentionItem but not in Go attentionItem",
    ).toEqual([]);
  });
});
