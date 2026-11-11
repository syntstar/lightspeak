import { describe, expect, it } from "vitest";
import go from "../../../server/internal/server/channelrules.go?raw";
import { ICON_CHOICES, channelIcon } from "./channelIcons";
import Hash from "@lucide/svelte/icons/hash";
import Volume2 from "@lucide/svelte/icons/volume-2";

describe("channel icons", () => {
  it("match the server allowlist", () => {
    for (const kind of ["text", "voice"] as const) {
      const m = new RegExp('"' + kind + '":\\s*[{]([^}]*)[}]').exec(go);
      const server = [...m![1]!.matchAll(/"([^"]+)"/g)].map((x) => x[1]);
      expect(ICON_CHOICES[kind].slice(1)).toEqual(server);
    }
  });
  it("fall back to # and the speaker", () => {
    expect(channelIcon("text", undefined)).toBe(Hash);
    expect(channelIcon("voice", "")).toBe(Volume2);
    expect(channelIcon("text", "headphones")).toBe(Hash); // voice-only icon
  });
});
