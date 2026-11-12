import { describe, expect, it, vi, afterEach } from "vitest";
import { Api } from "../api/client";
import { KeySession } from "./keySession";

afterEach(() => vi.unstubAllGlobals());

describe("key operation session isolation", () => {
  it("rejects an old native result after logout", async () => {
    const source = new Api("https://a.test");
    source.token = "old";
    const life = new AbortController();
    const scope = new KeySession(source, () => true, life.signal);
    let finish!: (s: string) => void;
    const result = scope.run(
      () =>
        new Promise<string>((resolve) => {
          finish = resolve;
        }),
    );
    life.abort();
    finish("old media key");
    await expect(result).rejects.toMatchObject({ code: "session_ended" });
  });

  it("never sends an old operation to a newly selected server", async () => {
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    const source = new Api("https://a.test");
    source.token = "old";
    const scope = new KeySession(source, () => true, new AbortController().signal);
    source.base = "https://b.test";
    source.token = "new";
    await expect(scope.post("/e2ee/1/envelopes", {})).rejects.toMatchObject({ code: "session_ended" });
    expect(fetch).not.toHaveBeenCalled();
  });

  it("rejects a replaced cookie session on the same origin", async () => {
    const source = new Api("https://a.test");
    source.cookieSession = true;
    let current = true;
    const scope = new KeySession(source, () => current, new AbortController().signal);
    current = false;
    await expect(scope.get("/e2ee/1/devices")).rejects.toMatchObject({ code: "session_ended" });
  });
});
