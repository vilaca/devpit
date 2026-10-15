import { describe, it, expect } from "vitest";
import {
  isMine,
  isAssignedNotAuthored,
  isReviewer,
  matchesFilter,
  visibleBuckets,
  parseFilter,
  partitionVisible,
  scopeToConnection,
} from "./buckets";
import { makeItem, makeConnection } from "./fixtures";

describe("isMine", () => {
  const conns = [makeConnection({ id: "c1", identity: "me" })];

  it("is true when the connection identity matches the author", () => {
    expect(isMine(makeItem({ connection_id: "c1", author: "me" }), conns)).toBe(
      true,
    );
  });

  it("is false when the author differs", () => {
    expect(
      isMine(makeItem({ connection_id: "c1", author: "other" }), conns),
    ).toBe(false);
  });

  it("is false with no author", () => {
    expect(isMine(makeItem({ connection_id: "c1", author: "" }), conns)).toBe(
      false,
    );
  });

  it("is false with empty connections", () => {
    expect(isMine(makeItem({ author: "me" }), [])).toBe(false);
  });

  it("is true for an assignee on someone else's item", () => {
    expect(
      isMine(
        makeItem({
          connection_id: "c1",
          author: "other",
          my_roles: ["assignee"],
        }),
        conns,
      ),
    ).toBe(true);
  });
});

// The badge's whole job is the case isMine folds in: before this helper the row
// gated it on !isMine, which is false for every assignee, so it never rendered.
describe("isAssignedNotAuthored", () => {
  const conns = [makeConnection({ id: "c1", identity: "me" })];

  it("is true for an assignee on someone else's item — and that item is mine", () => {
    const item = makeItem({
      connection_id: "c1",
      author: "other",
      my_roles: ["assignee"],
    });
    expect(isAssignedNotAuthored(item, conns)).toBe(true);
    expect(isMine(item, conns)).toBe(true);
  });

  it("is false when you authored the item you're assigned to", () => {
    expect(
      isAssignedNotAuthored(
        makeItem({ connection_id: "c1", author: "me", my_roles: ["assignee"] }),
        conns,
      ),
    ).toBe(false);
  });

  it("is false when you aren't an assignee", () => {
    expect(
      isAssignedNotAuthored(
        makeItem({
          connection_id: "c1",
          author: "other",
          my_roles: ["reviewer"],
        }),
        conns,
      ),
    ).toBe(false);
  });

  it("is true for an assignee when authorship can't be resolved", () => {
    expect(
      isAssignedNotAuthored(
        makeItem({ author: "", my_roles: ["assignee"] }),
        [],
      ),
    ).toBe(true);
  });
});

describe("isReviewer", () => {
  it("is true when my_roles carries reviewer (requested, not yet reviewed)", () => {
    expect(
      isReviewer(makeItem({ my_roles: ["reviewer"], my_review_state: "" })),
    ).toBe(true);
  });

  it("falls back to my_review_state when the role was dropped after review", () => {
    expect(
      isReviewer(makeItem({ my_roles: [], my_review_state: "approved" })),
    ).toBe(true);
  });

  it("is false with neither reviewer role nor a review state", () => {
    expect(isReviewer(makeItem({ my_roles: ["author"] }))).toBe(false);
  });
});

describe("matchesFilter", () => {
  const conns = [makeConnection({ id: "c1", identity: "me" })];

  it("matches everything when the filter is null", () => {
    expect(matchesFilter(makeItem(), null)).toBe(true);
  });

  it("mine matches on authorship via connection identity", () => {
    expect(
      matchesFilter(
        makeItem({ connection_id: "c1", author: "me" }),
        "mine",
        conns,
      ),
    ).toBe(true);
  });

  it("mentioned folds in the review plate as well as the mentioned state", () => {
    expect(
      matchesFilter(makeItem({ states: ["mentioned"] }), "mentioned"),
    ).toBe(true);
    expect(
      matchesFilter(makeItem({ my_review_state: "reviewed" }), "mentioned"),
    ).toBe(true);
    expect(matchesFilter(makeItem(), "mentioned")).toBe(false);
  });

  it("plain states match on the states list", () => {
    expect(
      matchesFilter(
        makeItem({ states: ["review_requested"] }),
        "review_requested",
      ),
    ).toBe(true);
    expect(matchesFilter(makeItem({ states: [] }), "review_requested")).toBe(
      false,
    );
  });
});

describe("visibleBuckets", () => {
  const conns = [makeConnection({ id: "c1", identity: "me" })];

  it("surfaces Mine first, then non-empty signal buckets, omitting empty ones", () => {
    const items = [
      makeItem({
        id: "a",
        connection_id: "c1",
        author: "me",
        states: ["ready_to_merge"],
      }),
      makeItem({ id: "b", author: "other", states: ["review_requested"] }),
    ];
    const buckets = visibleBuckets(items, conns);
    expect(buckets.map((b) => b.key)).toEqual([
      "mine",
      "review_requested",
      "ready_to_merge",
    ]);
    expect(buckets.find((b) => b.key === "mine")?.count).toBe(1);
  });

  it("excludes flagged items from bucket counts", () => {
    const items = [
      makeItem({ id: "a", flagged: true, states: ["review_requested"] }),
    ];
    expect(visibleBuckets(items, conns)).toEqual([]);
  });
});

describe("partitionVisible", () => {
  const items = [
    makeItem({ id: "p", flagged: true, states: ["blocked"] }),
    makeItem({ id: "a", states: ["blocked"] }),
    makeItem({ id: "b", states: ["review_requested"] }),
  ];

  it("on the All view lifts flagged items into the pinned zone", () => {
    const { pinned, ranked } = partitionVisible(items, null, []);
    expect(pinned.map((i) => i.id)).toEqual(["p"]);
    expect(ranked.map((i) => i.id)).toEqual(["a", "b"]);
  });

  it("under a filter hides the pinned zone and folds matching pins into ranked", () => {
    const { pinned, ranked } = partitionVisible(items, "blocked", []);
    expect(pinned).toEqual([]);
    expect(ranked.map((i) => i.id)).toEqual(["p", "a"]);
  });
});

describe("parseFilter", () => {
  it("accepts known signal states and mine", () => {
    expect(parseFilter("review_requested")).toBe("review_requested");
    expect(parseFilter("mine")).toBe("mine");
    expect(parseFilter("checking")).toBe("checking");
  });

  it("rejects unknown values and null", () => {
    expect(parseFilter("garbage")).toBeNull();
    expect(parseFilter("")).toBeNull();
    expect(parseFilter(null)).toBeNull();
  });
});

describe("scopeToConnection", () => {
  const conns = [
    makeConnection({ id: "c1", identity: "me" }),
    makeConnection({ id: "c2", identity: "me" }),
  ];
  const items = [
    makeItem({ id: "a", connection_id: "c1" }),
    makeItem({ id: "b", connection_id: "c2", flagged: true }),
    makeItem({ id: "c", connection_id: "c2" }),
  ];

  it("keeps only the selected connection's items, pinned ones included", () => {
    const s = scopeToConnection(items, conns, "c2");
    expect(s.items.map((i) => i.id)).toEqual(["b", "c"]);
    expect(s.connections.map((c) => c.id)).toEqual(["c2"]);
    expect(s.connectionId).toBe("c2");
  });

  it("keeps the ranked order when connections interleave", () => {
    // Scoping is a filter, never a reorder (INV-6).
    const mixed = [
      makeItem({ id: "w", connection_id: "c1" }),
      makeItem({ id: "x", connection_id: "c2" }),
      makeItem({ id: "y", connection_id: "c1" }),
      makeItem({ id: "z", connection_id: "c2" }),
    ];
    expect(
      scopeToConnection(mixed, conns, "c2").items.map((i) => i.id),
    ).toEqual(["x", "z"]);
  });

  it("is unscoped for null (All)", () => {
    const s = scopeToConnection(items, conns, null);
    expect(s.items).toBe(items);
    expect(s.connections).toBe(conns);
    expect(s.connectionId).toBeNull();
  });

  it("falls back to unscoped for an id no configured connection has", () => {
    // A stale ?conn= must not render an empty list that reads as "all clear".
    const s = scopeToConnection(items, conns, "gone");
    expect(s.items).toBe(items);
    expect(s.connectionId).toBeNull();
  });

  it("yields no items for a known connection with nothing open", () => {
    const s = scopeToConnection(
      items,
      [...conns, makeConnection({ id: "c3" })],
      "c3",
    );
    expect(s.items).toEqual([]);
    expect(s.connections.map((c) => c.id)).toEqual(["c3"]);
  });
});
