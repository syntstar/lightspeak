import { afterEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
  identity: {} as object,
  listeners: [] as ((op: string, data: unknown) => void)[],
  open: vi.fn(),
}));

vi.mock("../native", () => ({ hasStore: true, native: { e2eeOpen: state.open } }));
vi.mock("../state/app.svelte", async () => {
  const { Api } = await import("../api/client");
  const api = new Api("https://a.test");
  api.token = "token";
  return {
    api,
    app: {
      get sessionIdentity() {
        return state.identity;
      },
      on: (fn: (op: string, data: unknown) => void) => state.listeners.push(fn),
    },
  };
});
vi.mock("../prefs.svelte", () => ({ savePrefs: vi.fn(), serverPrefs: vi.fn() }));
vi.mock("../i18n/index.svelte", () => ({ t: (s: string) => s }));
vi.mock("../ui/toast.svelte", () => ({ toast: vi.fn() }));

import { keys } from "./e2ee.svelte";

function endSession() {
  for (const fn of state.listeners) fn("session_end", null);
  state.identity = {};
}

afterEach(() => {
  endSession();
  state.open.mockReset();
  vi.unstubAllGlobals();
});

describe("media-key manager lifetime", () => {
  it("refetches identical container IDs after switching accounts", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ ciphertext: "sealed", currentEpoch: 1 })));
    vi.stubGlobal("fetch", fetch);
    state.open.mockResolvedValueOnce(btoa("a".repeat(32))).mockResolvedValueOnce(btoa("b".repeat(32)));
    expect((await keys.key("1", 1))[0]).toBe(97);
    expect((await keys.key("1", 1))[0]).toBe(97);
    expect(fetch).toHaveBeenCalledTimes(1);
    endSession();
    expect((await keys.key("1", 1))[0]).toBe(98);
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("does not repopulate the cache with a late native result", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify({ ciphertext: "sealed", currentEpoch: 9 }))),
    );
    let finish!: (value: string) => void;
    state.open.mockImplementationOnce(
      () =>
        new Promise<string>((resolve) => {
          finish = resolve;
        }),
    );
    const pending = keys.key("1", 9);
    const rejected = expect(pending).rejects.toMatchObject({ code: "session_ended" });
    await vi.waitFor(() => expect(state.open).toHaveBeenCalledTimes(1));
    endSession();
    finish(btoa("a".repeat(32)));
    await rejected;
    expect(keys.latestEpoch("1")).toBe(0);
    state.open.mockResolvedValueOnce(btoa("b".repeat(32)));
    expect((await keys.key("1", 9))[0]).toBe(98);
  });
});
