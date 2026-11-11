import { describe, expect, it, vi } from "vitest";
import type { Channel, Space } from "./api/types";
import { P, accessFor } from "./perms";
import {
  allowedRoles,
  awayTarget,
  channelPerms,
  overrideOf,
  restrictedForAll,
  setTri,
  slowFor,
  triOf,
} from "./channelRules";
import { AwayWatch, type AwayHost } from "./voice/away";

const sp = {
  id: "s",
  name: "S",
  ownerId: "o",
  everyonePerms: P.view | P.connect | P.speak | P.stream | P.send | P.attach | P.react,
  roles: [
    { id: "admin", name: "Admin", position: 3, permissions: P.administrator },
    { id: "mod", name: "Mod", position: 2, permissions: P.manageMessages },
    { id: "host", name: "Host", position: 1, permissions: 0 },
  ],
} as unknown as Space;

const ch = (o: Partial<Channel>): Channel => ({
  id: "c",
  spaceId: "s",
  kind: "text",
  name: "c",
  topic: "",
  position: 0,
  ...o,
});
const member = accessFor(sp, "u", [], false);
const host = accessFor(sp, "u", ["host"], false);
const admin = accessFor(sp, "u", ["admin"], false);
const mod = accessFor(sp, "u", ["mod"], false);

const may = (c: Channel, a: ReturnType<typeof accessFor>, ids: string[], bit: number) =>
  (channelPerms(c, a, ids) & bit) === bit;

describe("channel permissions", () => {
  it("defaults to the space-wide value", () => {
    expect(may(ch({}), member, [], P.send)).toBe(true);
    expect(may(ch({}), member, [], P.mentionEveryone)).toBe(false);
  });

  it("read-only channel: @everyone denied, a role allowed (allow wins)", () => {
    const c = ch({
      overrides: [
        { roleId: "s", allow: 0, deny: P.send },
        { roleId: "host", allow: P.send, deny: 0 },
        { roleId: "mod", allow: 0, deny: P.send },
      ],
    });
    expect(may(c, member, [], P.send)).toBe(false);
    expect(may(c, host, ["host"], P.send)).toBe(true);
    expect(may(c, accessFor(sp, "u", ["host", "mod"], false), ["host", "mod"], P.send)).toBe(true);
    expect(may(c, admin, ["admin"], P.send)).toBe(true);
    expect(restrictedForAll(c, P.send)).toBe(true);
    expect(allowedRoles(sp, c, P.send)).toEqual(["Host"]);
  });

  it("a channel can allow what the space denies", () => {
    const noSend = { ...sp, everyonePerms: P.view | P.connect } as Space;
    const m = accessFor(noSend, "u", [], false);
    const c = ch({ overrides: [{ roleId: "s", allow: P.send, deny: 0 }] });
    expect(may(ch({}), m, [], P.send)).toBe(false);
    expect(may(c, m, [], P.send)).toBe(true);
  });

  it("private channel: without View nothing applies", () => {
    const c = ch({
      overrides: [
        { roleId: "s", allow: 0, deny: P.view },
        { roleId: "host", allow: P.view, deny: 0 },
      ],
    });
    expect(channelPerms(c, member, [])).toBe(0);
    expect(may(c, host, ["host"], P.view | P.send)).toBe(true);
    expect(may(c, admin, ["admin"], P.view)).toBe(true);
    expect(restrictedForAll(c, P.view)).toBe(true);
    const away = ch({ kind: "voice", mode: "away", overrides: [{ roleId: "s", allow: 0, deny: P.view }] });
    expect(may(away, member, [], P.view | P.connect)).toBe(true);
  });

  it("nobody speaks or streams in the away channel", () => {
    const away = ch({ kind: "voice", mode: "away" });
    expect(may(away, admin, ["admin"], P.speak)).toBe(false);
    expect(may(away, admin, ["admin"], P.stream)).toBe(false);
    expect(may(away, member, [], P.connect)).toBe(true);
    const lockedAway = ch({
      kind: "voice",
      mode: "away",
      overrides: [{ roleId: "s", allow: 0, deny: P.connect }],
    });
    expect(may(lockedAway, member, [], P.connect)).toBe(true);
  });

  it("tri-state editing", () => {
    let o = overrideOf(ch({}), "host");
    expect(triOf(o, P.speak)).toBe("inherit");
    o = setTri(o, P.speak, "deny");
    expect(triOf(o, P.speak)).toBe("deny");
    o = setTri(o, P.speak, "allow");
    expect(o).toEqual({ roleId: "host", allow: P.speak, deny: 0 });
    expect(setTri(o, P.speak, "inherit")).toEqual({ roleId: "host", allow: 0, deny: 0 });
  });

  it("slow mode spares moderators", () => {
    const c = ch({ slowmode: 30 });
    expect(slowFor(c, member)).toBe(30);
    expect(slowFor(c, mod)).toBe(0);
    expect(slowFor(ch({}), member)).toBe(0);
  });

  it("finds the away channel only when it moves people", () => {
    expect(awayTarget([ch({ kind: "voice", mode: "away" })])).toBeUndefined();
    expect(awayTarget([ch({ id: "a", kind: "voice", mode: "away", awayAfter: 5 })])?.id).toBe("a");
  });
});

describe("AwayWatch", () => {
  const away = ch({ id: "afk", kind: "voice", mode: "away", awayAfter: 5 });
  const lounge = ch({ id: "lounge", kind: "voice" });
  function setup(osIdle: number | null, streaming = false) {
    const move = vi.fn();
    const host: AwayHost = {
      channelId: () => "lounge",
      channel: (id) => [away, lounge].find((c) => c.id === id),
      spaceChannels: () => [lounge, away],
      streaming: () => streaming,
      osIdle: () => Promise.resolve(osIdle),
      move,
    };
    const w = new AwayWatch(host, false);
    return { w, move };
  }
  const MIN = 60_000;

  it("moves after the idle time without speaking or input", async () => {
    const { w, move } = setup(6 * 60);
    w.active(0);
    await w.check(4 * MIN);
    expect(move).not.toHaveBeenCalled();
    await w.check(6 * MIN);
    expect(move).toHaveBeenCalledWith(away);
  });

  it("recent system input keeps you", async () => {
    const { w, move } = setup(30);
    w.active(0);
    await w.check(10 * MIN);
    expect(move).not.toHaveBeenCalled();
  });

  it("speaking keeps you, and a running stream too", async () => {
    const a = setup(null);
    a.w.active(0);
    a.w.active(4 * MIN);
    await a.w.check(6 * MIN);
    expect(a.move).not.toHaveBeenCalled();
    const b = setup(null, true);
    b.w.active(0);
    await b.w.check(60 * MIN);
    expect(b.move).not.toHaveBeenCalled();
  });
});
