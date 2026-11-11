import { describe, expect, it } from "vitest";
import { parseSearch, searchParams } from "./search";

const r = {
  user: (n: string) => ({ anna: "1", "anna b": "2" })[n.toLowerCase()],
  channel: (n: string) => ({ allgemein: "10" })[n.toLowerCase()],
};

describe("parseSearch", () => {
  it("keeps plain text", () => {
    expect(parseSearch("  hello   world ", r)).toEqual({ text: "hello world", unresolved: [] });
  });

  it("resolves filters in English and German", () => {
    const f = parseSearch("from:anna in:#allgemein has:file pizza", r);
    expect(f).toEqual({ text: "pizza", authorId: "1", channelId: "10", hasFile: true, unresolved: [] });
    expect(parseSearch("von:@Anna hat:datei", r)).toMatchObject({ authorId: "1", hasFile: true, text: "" });
  });

  it("supports quoted names", () => {
    expect(parseSearch('from:"Anna B" x', r)).toMatchObject({ authorId: "2", text: "x" });
  });

  it("reports unknown filters instead of silently dropping them", () => {
    const f = parseSearch("from:bob has:video hi", r);
    expect(f.authorId).toBeUndefined();
    expect(f.hasFile).toBeUndefined();
    expect(f.unresolved).toEqual(["from:bob", "has:video"]);
    expect(f.text).toBe("hi");
  });

  it("does not treat words containing a colon mid-word as filters", () => {
    expect(parseSearch("http://x.de/in:y", r).text).toBe("http://x.de/in:y");
  });
});

describe("searchParams", () => {
  it("returns null for an empty query", () => {
    expect(searchParams({ text: "", unresolved: [] }, "5")).toBeNull();
  });

  it("scopes to the space unless a channel is given", () => {
    expect(searchParams({ text: "a b", unresolved: [] }, "5")).toBe("q=a+b&spaceId=5");
    expect(searchParams({ text: "", channelId: "10", hasFile: true, unresolved: [] }, "5")).toBe(
      "channelId=10&has=file",
    );
  });
});
