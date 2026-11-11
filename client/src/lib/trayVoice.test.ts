import { describe, expect, it } from "vitest";
import type { VoiceState } from "./api/types";
import { MAX_TRAY_VOICE, trayVoiceRows } from "./trayVoice";

const vs = (userId: string, channelId: string | null): VoiceState => ({
  userId,
  channelId,
  muted: false,
  deafened: false,
  streaming: false,
  serverMuted: false,
});

const channels: Record<string, { name: string; space: string }> = {
  c1: { name: "Gaming", space: "Friends" },
  c2: { name: "Coding", space: "Friends & Co" },
};
const names: Record<string, string> = {
  u1: "Ben",
  u2: "Anna",
  u3: "Carl",
  u4: "Dora",
  u5: "Emil",
  u6: "Fritz",
};
const channel = (id: string) => channels[id] ?? null;
const userName = (id: string) => names[id] ?? "?";

describe("trayVoiceRows", () => {
  it("groups by channel, own channel first, escapes &", () => {
    const rows = trayVoiceRows(
      [vs("u1", "c1"), vs("u2", "c1"), vs("u3", "c2"), vs("u4", null)],
      channel,
      userName,
      "c2",
    );
    expect(rows).toEqual([
      { id: "c2", label: "Friends && Co · Coding: Carl", current: true },
      { id: "c1", label: "Friends · Gaming: Anna, Ben", current: false },
    ]);
  });

  it("skips unknown channels (DM calls) and shortens long lists", () => {
    const states = ["u1", "u2", "u3", "u4", "u5", "u6"].map((u) => vs(u, "c1"));
    states.push(vs("u9", "dm1"));
    const rows = trayVoiceRows(states, channel, userName, null);
    expect(rows).toHaveLength(1);
    expect(rows[0]!.label).toBe("Friends · Gaming: Anna, Ben, Carl, Dora +2");
  });

  it("caps rows and label length", () => {
    const many: Record<string, { name: string; space: string }> = {};
    const states: VoiceState[] = [];
    for (let i = 0; i < 20; i++) {
      many[`k${i}`] = { name: "x".repeat(100), space: "S" };
      states.push(vs(`u${i}`, `k${i}`));
    }
    const rows = trayVoiceRows(
      states,
      (id) => many[id] ?? null,
      () => "N",
      null,
    );
    expect(rows).toHaveLength(MAX_TRAY_VOICE);
    expect(rows[0]!.label.length).toBeLessThanOrEqual(80);
  });
});
