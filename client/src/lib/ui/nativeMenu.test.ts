import { describe, expect, it } from "vitest";
import { allowNativeMenu } from "./nativeMenu";

/** Fake element whose ancestors match the given selector parts. */
const el = (...matches: string[]) => ({
  closest: (sel: string) => (sel.split(", ").some((s) => matches.some((m) => s.includes(m))) ? {} : null),
});

describe("allowNativeMenu", () => {
  it("blocks the menu on plain UI", () => {
    expect(allowNativeMenu(el(), "", false)).toBe(false);
    expect(allowNativeMenu(null, "", false)).toBe(false);
  });
  it("allows text fields, editable content and selections", () => {
    expect(allowNativeMenu(el("input"), "", false)).toBe(true);
    expect(allowNativeMenu(el("textarea"), "", false)).toBe(true);
    expect(allowNativeMenu(el("contenteditable='true'"), "", false)).toBe(true);
    expect(allowNativeMenu(el(), "some text", false)).toBe(true);
    expect(allowNativeMenu(el(), "   ", false)).toBe(false);
  });
  it("allows links only in the browser client", () => {
    expect(allowNativeMenu(el("a[href]"), "", true)).toBe(true);
    expect(allowNativeMenu(el("a[href]"), "", false)).toBe(false);
  });
});
