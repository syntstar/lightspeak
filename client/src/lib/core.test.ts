import { describe, expect, it } from "vitest";
import { de } from "./i18n/de";
import { en } from "./i18n/en";
import { format, i18n, systemLocale, t, terr, tp } from "./i18n/index.svelte";
import { DEFAULT_PREFS, sanitizePrefs } from "./prefs.svelte";
import { fuzzyScore, idCmp, idGt } from "./state/ids";
import { versionLess } from "./state/app.svelte";
import {
  contentMode,
  degradation,
  maxBitrate,
  previewEncoding,
  videoConstraints,
} from "./voice/streamPresets";

describe("i18n", () => {
  it("has the same keys and placeholders in both languages", () => {
    expect(Object.keys(de).sort()).toEqual(Object.keys(en).sort());
    const ph = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();
    for (const k of Object.keys(en) as (keyof typeof en)[]) {
      expect(ph(de[k]), k).toEqual(ph(en[k]));
    }
  });

  it("picks the system language", () => {
    expect(systemLocale(["de-DE", "en"])).toBe("de");
    expect(systemLocale(["de-AT"])).toBe("de");
    expect(systemLocale(["fr-FR", "de"])).toBe("en");
    expect(systemLocale([])).toBe("en");
  });

  it("formats, pluralises and translates error codes", () => {
    expect(format("a {x} b {y}", { x: 1 })).toBe("a 1 b {y}");
    i18n.set("de");
    expect(tp("auth.inviteMembers", 1)).toBe("1 Mitglied");
    expect(tp("auth.inviteMembers", 3)).toBe("3 Mitglieder");
    expect(terr("invite_expired")).toBe(de["err.invite_expired"]);
    expect(terr("something_new")).toBe(de["err.unknown"]);
    i18n.set("en");
    expect(t("members.online", { count: 2 })).toBe("Online – 2");
    expect(tp("auth.inviteMembers", 1)).toBe("1 member");
  });
});

describe("ids", () => {
  it("compares snowflakes as big numbers", () => {
    expect(idCmp("9", "10")).toBeLessThan(0);
    expect(idCmp("123456789012345678", "123456789012345679")).toBeLessThan(0);
    expect(idGt("200", "199")).toBe(true);
    expect(idGt(undefined, "1")).toBe(false);
    expect(idGt("1", undefined)).toBe(true);
  });

  it("fuzzy matches in order and ranks prefixes higher", () => {
    expect(fuzzyScore("gen", "general")).toBeGreaterThan(fuzzyScore("gen", "a-generic-thing"));
    expect(fuzzyScore("gnrl", "general")).toBeGreaterThan(0);
    expect(fuzzyScore("xyz", "general")).toBe(-1);
    expect(fuzzyScore("", "x")).toBe(0);
  });
});

describe("prefs", () => {
  it("keeps valid values and drops invalid ones", () => {
    const p = sanitizePrefs({
      theme: "dark",
      accent: "pink",
      language: "xx",
      chatFontSize: 30,
      mica: false,
      collapsed: { a: true, b: "yes" },
      server: "https://chat.example.org",
      servers: {
        "https://chat.example.org": {
          name: "Freunde",
          devices: { anna: { deviceId: "1", publicKey: "k" }, bad: { deviceId: 2 } },
          lastSpace: "7",
        },
        "http://evil.example": { name: "plain http" },
        "not a url": {},
      },
    });
    expect(p.theme).toBe("dark");
    expect(p.accent).toBe("pink");
    expect(p.language).toBe(DEFAULT_PREFS.language);
    expect(p.chatFontSize).toBe(DEFAULT_PREFS.chatFontSize);
    expect(p.mica).toBe(false);
    expect(p.collapsed).toEqual({ a: true });
    expect(p.server).toBe("https://chat.example.org");
    expect(Object.keys(p.servers)).toEqual(["https://chat.example.org"]);
    const sp = p.servers["https://chat.example.org"]!;
    expect(sp.name).toBe("Freunde");
    expect(sp.lastSpace).toBe("7");
    expect(sp.devices).toEqual({ anna: { deviceId: "1", publicKey: "k" } });
    expect(sanitizePrefs({ server: "http://evil.example" }).server).toBe("");
    expect(sanitizePrefs(null)).toEqual(DEFAULT_PREFS);
    expect(DEFAULT_PREFS.autoUpdate).toBe(true);
    expect(sanitizePrefs({ autoUpdate: false }).autoUpdate).toBe(false);
    expect(sanitizePrefs({ autoUpdate: "no" }).autoUpdate).toBe(true);
  });

  it("sanitizes audio, per-user volumes and known keys", () => {
    const key = "A".repeat(43) + "=";
    const p = sanitizePrefs({
      audio: {
        mode: "ptt",
        pttKeys: [0x7c],
        muteKeys: [1, 2, 3, 4, 5],
        inputVolume: 999,
        noise: "max",
        pttDelay: -5,
      },
      userVolumes: { "123": 250, abc: 50, "7": 40 },
      localMutes: { "9": true, "8": "yes" },
      servers: { "https://a.org": { knownKeys: { "5": [key, "not a key", 3], x: [key] } } },
    });
    expect(p.audio.mode).toBe("ptt");
    expect(p.audio.pttKeys).toEqual([0x7c]);
    expect(p.audio.muteKeys).toEqual([]); // more than 4 keys rejected
    expect(p.audio.inputVolume).toBe(200);
    expect(p.audio.noise).toBe("standard");
    expect(p.audio.pttDelay).toBe(0);
    expect(p.userVolumes).toEqual({ "123": 200, "7": 40 });
    expect(p.localMutes).toEqual({ "9": true });
    expect(p.servers["https://a.org"]!.knownKeys).toEqual({ "5": [key] });
  });
});

describe("stream presets", () => {
  it("scales the preview layer to 360p and caps it at 15 fps", () => {
    expect(previewEncoding(1920, 1080, 60)).toEqual({
      scaleResolutionDownBy: 3,
      maxBitrate: 400_000,
      maxFramerate: 15,
    });
    expect(previewEncoding(3440, 1440, 30).scaleResolutionDownBy).toBe(4);
    expect(previewEncoding(800, 300, 15).scaleResolutionDownBy).toBe(1);
    expect(previewEncoding(0, 0, 30).scaleResolutionDownBy).toBe(1);
  });

  it("maps quality to bitrate, constraints and content mode", () => {
    expect(maxBitrate({ resolution: "1080", fps: 60 })).toBe(8_000_000);
    expect(maxBitrate({ resolution: "720", fps: 30 })).toBe(2_500_000);
    expect(maxBitrate({ resolution: "source", fps: 60 })).toBe(15_000_000);
    expect(videoConstraints({ resolution: "1440", fps: 60 })).toEqual({
      frameRate: { ideal: 60, max: 60 },
      width: { max: 2560 },
      height: { max: 1440 },
    });
    expect(videoConstraints({ resolution: "source", fps: 30 }).width).toBeUndefined();
    expect(contentMode({ content: "auto", fps: 60 })).toBe("motion");
    expect(contentMode({ content: "auto", fps: 30 })).toBe("detail");
    expect(contentMode({ content: "motion", fps: 15 })).toBe("motion");
    expect(degradation({ content: "detail", fps: 60 })).toBe("maintain-resolution");
    expect(degradation({ content: "auto", fps: 60 })).toBe("maintain-framerate");
  });

  it("sanitizes stored stream settings", () => {
    const p = sanitizePrefs({
      stream: { surface: "monitor", resolution: "4k", fps: 60, content: "detail", audio: "yes" },
      streamVolumes: { "12": 300, x: 5 },
    });
    expect(p.stream).toEqual({ ...DEFAULT_PREFS.stream, surface: "monitor", fps: 60, content: "detail" });
    expect(p.streamVolumes).toEqual({ "12": 200 });
  });
});

describe("versions", () => {
  it("compares dotted versions numerically", () => {
    expect(versionLess("0.1.9", "0.1.10")).toBe(true);
    expect(versionLess("0.2.0", "0.1.10")).toBe(false);
    expect(versionLess("1.0", "1.0.0")).toBe(false);
  });
});
