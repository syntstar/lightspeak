import { describe, expect, it } from "vitest";
import { DEFAULT_LIMITS, limitParams, readLimits } from "./limits";

describe("server limits", () => {
  it("falls back to the defaults for older servers", () => {
    expect(readLimits(undefined)).toEqual(DEFAULT_LIMITS);
    expect(readLimits({})).toEqual(DEFAULT_LIMITS);
  });

  it("takes the operator's values and ignores nonsense", () => {
    expect(readLimits({ messageChars: 10000, invites: "admins" })).toMatchObject({
      messageChars: 10000,
      uploadBytes: DEFAULT_LIMITS.uploadBytes,
      invites: "admins",
    });
    const bad = { messageChars: -1, spaceRoles: 0, invites: "x" } as unknown as Parameters<
      typeof readLimits
    >[0];
    expect(readLimits(bad)).toEqual(DEFAULT_LIMITS);
  });

  it("offers the values error messages show", () => {
    expect(limitParams(DEFAULT_LIMITS)).toEqual({ maxChars: 4000, maxMB: 200, maxRoles: 25 });
    expect(limitParams(readLimits({ uploadBytes: 500 << 20 })).maxMB).toBe(500);
  });
});
