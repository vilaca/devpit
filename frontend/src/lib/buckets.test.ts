import { describe, it, expect } from "vitest";
import {
  isMine,
  isReviewer,
  matchesFilter,
  visibleBuckets,
  parseFilter,
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
