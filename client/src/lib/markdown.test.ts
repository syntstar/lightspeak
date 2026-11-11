import { describe, expect, it } from "vitest";
import { parse, safeUrl, toPlain, type MdNode } from "./markdown";

const flat = (nodes: MdNode[]): string => JSON.stringify(nodes);

describe("markdown", () => {
  it("renders the supported subset", () => {
    expect(parse("**fett** *kursiv* ~~durch~~ `code` ||geheim||")).toEqual([
      { t: "b", c: [{ t: "text", v: "fett" }] },
      { t: "text", v: " " },
      { t: "i", c: [{ t: "text", v: "kursiv" }] },
      { t: "text", v: " " },
      { t: "s", c: [{ t: "text", v: "durch" }] },
      { t: "text", v: " " },
      { t: "code", v: "code" },
      { t: "text", v: " " },
      { t: "spoiler", c: [{ t: "text", v: "geheim" }] },
    ]);
    expect(parse("```js\nlet a = 1;\n```")).toEqual([{ t: "pre", lang: "js", v: "let a = 1;" }]);
    expect(parse("> zitat\n> zwei")[0]).toMatchObject({ t: "quote" });
    expect(parse("hi <@123> @everyone")).toEqual([
      { t: "text", v: "hi " },
      { t: "mention", id: "123" },
      { t: "text", v: " " },
      { t: "everyone" },
    ]);
    expect(parse("snake_case_name")).toEqual([{ t: "text", v: "snake_case_name" }]);
    expect(parse("a\nb")).toEqual([{ t: "text", v: "a" }, { t: "br" }, { t: "text", v: "b" }]);
  });

  it("never produces HTML from input", () => {
    const evil = [
      "<img src=x onerror=alert(1)>",
      "<script>alert(1)</script>",
      '<a href="javascript:alert(1)">x</a>',
      "<svg/onload=alert(1)>",
    ];
    for (const e of evil) {
      const n = parse(e);
      expect(
        n.every((x) => x.t === "text"),
        e,
      ).toBe(true);
      expect(toPlain(n)).toBe(e);
    }
  });

  it("only allows http(s) links", () => {
    for (const bad of [
      "[x](javascript:alert(1))",
      "[x](JaVaScRiPt:alert(1))",
      "[x](data:text/html,<script>alert(1)</script>)",
      "[x](vbscript:msgbox)",
      "[x](file:///C:/Windows)",
      "[x](lightspeak://invite/abc)",
    ]) {
      expect(flat(parse(bad)), bad).not.toContain('"t":"link"');
    }
    expect(safeUrl("https://user:pw@evil.example/")).toBeNull();
    const ok = parse("[Seite](https://example.com/a?b=1) und https://example.org/x.");
    expect(ok[0]).toEqual({ t: "link", href: "https://example.com/a?b=1", c: [{ t: "text", v: "Seite" }] });
    expect(ok[2]).toEqual({
      t: "link",
      href: "https://example.org/x",
      c: [{ t: "text", v: "https://example.org/x" }],
    });
    expect(ok[3]).toEqual({ t: "text", v: "." });
  });

  it("strips bidi control characters", () => {
    expect(toPlain(parse("evil\u202Etxt.exe"))).toBe("eviltxt.exe");
    expect(toPlain(parse("a\u2066b\u2069c"))).toBe("abc");
  });

  it("stays fast on pathological input", () => {
    const cases = [
      "*".repeat(4000),
      "**a".repeat(1300),
      "_a ".repeat(1300),
      "[".repeat(4000),
      "`".repeat(4000),
      "||x".repeat(1300),
      "a".repeat(100_000),
      "*_~~||".repeat(700),
    ];
    for (const c of cases) {
      const t0 = performance.now();
      parse(c);
      expect(performance.now() - t0, c.slice(0, 12)).toBeLessThan(150);
    }
  });

  it("caps nesting depth", () => {
    const deep = "**".repeat(50) + "x" + "**".repeat(50);
    let depth = 0;
    const walk = (n: MdNode[], d: number) => {
      depth = Math.max(depth, d);
      for (const x of n) if ("c" in x) walk(x.c, d + 1);
    };
    walk(parse(deep), 0);
    expect(depth).toBeLessThanOrEqual(7);
  });
});
