import { describe, expect, it } from "vitest";
import type { Channel } from "./api/types";
import { reorder } from "./channelOrder";

const ch = (id: string, kind: Channel["kind"], position: number, parentId?: string): Channel =>
  ({ id, spaceId: "s", kind, name: id, topic: "", position, parentId }) as Channel;

// T: general, memes · V: lounge · loose text "news" on top
const all = [
  ch("T", "category", 0),
  ch("general", "text", 1, "T"),
  ch("memes", "text", 2, "T"),
  ch("V", "category", 3),
  ch("lounge", "voice", 4, "V"),
  ch("news", "text", 5),
];
const seq = (o: ReturnType<typeof reorder>) => o!.map((x) => `${x.id}${x.parentId ? "<" + x.parentId : ""}`);

describe("channel order", () => {
  it("moves a channel within its category", () => {
    expect(seq(reorder(all, { id: "memes", before: "general" }))).toEqual([
      "news<0",
      "T",
      "memes<T",
      "general<T",
      "V",
      "lounge<V",
    ]);
  });

  it("moves a channel into another category and out of all", () => {
    expect(seq(reorder(all, { id: "general", into: "V" }))).toContain("general<V");
    expect(seq(reorder(all, { id: "lounge", after: true, before: "news" }))).toEqual([
      "news<0",
      "lounge<0",
      "T",
      "general<T",
      "memes<T",
      "V",
    ]);
  });

  it("moves categories with their channels", () => {
    expect(seq(reorder(all, { id: "V", category: "T", after: false }))).toEqual([
      "news<0",
      "V",
      "lounge<V",
      "T",
      "general<T",
      "memes<T",
    ]);
  });

  it("ignores no-ops and impossible moves", () => {
    expect(reorder(all, { id: "general", before: "memes" })).toBeNull();
    expect(reorder(all, { id: "T", category: "T", after: true })).toBeNull();
    expect(reorder(all, { id: "T", into: "V" })).toBeNull();
    expect(reorder(all, { id: "general", before: "V" })).toBeNull();
    expect(reorder(all, { id: "nope", into: null })).toBeNull();
  });
});
