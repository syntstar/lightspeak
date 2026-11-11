import { describe, expect, it } from "vitest";
import { effectiveLevel, isMuted, sanitizeNotifyMap, shouldToast, type ToastInput } from "./notifyRules";

const text = { id: "10", spaceId: "1", kind: "text" };
const dm = { id: "20", kind: "dm" };

const base: ToastInput = {
  channel: text,
  authorId: "2",
  meId: "1",
  mentionsMe: false,
  myStatus: "online",
  viewing: false,
  authorBlocked: false,
  enabled: true,
};

describe("notify rules", () => {
  it("uses defaults: DMs all, spaces mentions", () => {
    expect(effectiveLevel({}, dm)).toBe("all");
    expect(effectiveLevel({}, text)).toBe("mentions");
  });

  it("channel overrides space", () => {
    expect(effectiveLevel({ "1": { level: "none" } }, text)).toBe("none");
    expect(effectiveLevel({ "1": { level: "none" }, "10": { level: "all" } }, text)).toBe("all");
  });

  it("space mute mutes its channels", () => {
    expect(isMuted({ "1": { muted: true } }, text)).toBe(true);
    expect(isMuted({ "1": { muted: true } }, dm)).toBe(false);
  });

  it("toasts mentions in spaces and every DM message", () => {
    expect(shouldToast({}, base)).toBe(false);
    expect(shouldToast({}, { ...base, mentionsMe: true })).toBe(true);
    expect(shouldToast({}, { ...base, channel: dm })).toBe(true);
    expect(shouldToast({ "10": { level: "all" } }, base)).toBe(true);
  });

  it("never toasts own, viewed, blocked, muted, DND or disabled", () => {
    const dmIn = { ...base, channel: dm };
    expect(shouldToast({}, { ...dmIn, authorId: "1" })).toBe(false);
    expect(shouldToast({}, { ...dmIn, viewing: true })).toBe(false);
    expect(shouldToast({}, { ...dmIn, authorBlocked: true })).toBe(false);
    expect(shouldToast({ "20": { muted: true } }, dmIn)).toBe(false);
    expect(shouldToast({}, { ...dmIn, myStatus: "dnd" })).toBe(false);
    expect(shouldToast({}, { ...dmIn, enabled: false })).toBe(false);
    expect(shouldToast({ "20": { level: "none" } }, dmIn)).toBe(false);
  });

  it("sanitizes synced data", () => {
    expect(
      sanitizeNotifyMap({
        "10": { level: "all", muted: true },
        "11": { level: "loud" },
        x: { muted: true },
        "12": null,
        "13": { muted: false },
      }),
    ).toEqual({ "10": { level: "all", muted: true } });
    expect(sanitizeNotifyMap("nope")).toEqual({});
  });
});
