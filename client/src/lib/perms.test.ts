import { describe, expect, it } from "vitest";
import { ALL, P, accessFor, canManageRole, grantable, has, outranks } from "./perms";

const sp = {
  ownerId: "o",
  everyonePerms: P.invite,
  roles: [
    { id: "admin", name: "Admin", position: 2, permissions: P.administrator },
    { id: "mod", name: "Mod", position: 1, permissions: P.kick | P.manageRoles },
  ],
};

describe("accessFor", () => {
  it("combines @everyone and roles", () => {
    const a = accessFor(sp, "u", ["mod"], false);
    expect(has(a, P.kick)).toBe(true);
    expect(has(a, P.invite)).toBe(true);
    expect(has(a, P.ban)).toBe(false);
    expect(a.rank).toBe(1);
  });
  it("gives owner, server admin and Administrator everything", () => {
    expect(accessFor(sp, "o", [], false).perms).toBe(ALL);
    expect(accessFor(sp, "x", [], true).perms).toBe(ALL);
    expect(accessFor(sp, "u", ["admin"], false).perms).toBe(ALL);
  });
  it("ranks server admin over owner over roles", () => {
    const host = accessFor(sp, "h", [], true);
    const owner = accessFor(sp, "o", [], false);
    const admin = accessFor(sp, "a", ["admin"], false);
    const mod = accessFor(sp, "m", ["mod"], false);
    const mod2 = accessFor(sp, "m2", ["mod"], false);
    expect(outranks(host, owner)).toBe(true);
    expect(outranks(owner, host)).toBe(false);
    expect(outranks(admin, mod)).toBe(true);
    expect(outranks(mod, mod2)).toBe(false);
    expect(outranks(mod, accessFor(sp, "u", [], false))).toBe(true);
  });
});

describe("role management", () => {
  const mod = accessFor(sp, "m", ["mod"], false);
  it("only below the own rank and with own permissions", () => {
    expect(canManageRole(mod, 0, P.kick)).toBe(true);
    expect(canManageRole(mod, 1, 0)).toBe(false);
    expect(canManageRole(mod, 0, P.ban)).toBe(false);
    expect(grantable(mod, P.kick | P.invite)).toBe(true);
    expect(grantable(mod, P.administrator)).toBe(false);
  });
});
