import { afterEach, describe, expect, it, vi } from "vitest";
import {
  fetchServerInfo,
  inviteLink,
  isInsecureAddress,
  normalizeServer,
  parseInvite,
  serverHost,
} from "./server";

describe("server addresses", () => {
  it("normalizes typed addresses to origins", () => {
    expect(normalizeServer("chat.example.org")).toBe("https://chat.example.org");
    expect(normalizeServer(" https://Chat.Example.org/ ")).toBe("https://chat.example.org");
    expect(normalizeServer("chat.example.org:8443")).toBe("https://chat.example.org:8443");
    expect(normalizeServer("https://chat.example.org/invite/AbCd1234")).toBe("https://chat.example.org");
    expect(normalizeServer("127.0.0.1:8080")).toBe("http://127.0.0.1:8080");
    expect(normalizeServer("http://localhost:8080")).toBe("http://localhost:8080");
  });

  it("refuses plain http on the network and odd input", () => {
    for (const bad of [
      "",
      "http://chat.example.org",
      "http://192.168.1.10:8080",
      "ftp://x.org",
      "https://user:pw@x.org",
      "javascript:alert(1)",
      "two words",
    ]) {
      expect(normalizeServer(bad), bad).toBeNull();
    }
    expect(isInsecureAddress("http://192.168.1.10:8080")).toBe(true);
    expect(isInsecureAddress("http://127.0.0.1:8080")).toBe(false);
    expect(serverHost("https://chat.example.org:8443")).toBe("chat.example.org:8443");
  });
});

describe("invites", () => {
  it("parses codes, web links and app links", () => {
    expect(parseInvite("AbCd1234")).toEqual({ code: "AbCd1234", server: null });
    expect(parseInvite("https://chat.example.org/invite/AbCd1234/")).toEqual({
      code: "AbCd1234",
      server: "https://chat.example.org",
    });
    expect(parseInvite("  https://x.org/invite/AbCd1234  ")?.server).toBe("https://x.org");
    expect(parseInvite("lightspeak://invite/chat.example.org/AbCd1234")).toEqual({
      code: "AbCd1234",
      server: "https://chat.example.org",
    });
    expect(parseInvite("lightspeak://invite/127.0.0.1:8080/AbCd1234")?.server).toBe("http://127.0.0.1:8080");
    expect(parseInvite(inviteLink("https://a.org", "AbCd1234"))?.server).toBe("https://a.org");
  });

  it("rejects malformed invites", () => {
    for (const bad of [
      "short",
      "https://x.org/invite/AbCd1234?x=1",
      "https://x.org/invite/AbCd1234#x",
      "http://x.org/invite/AbCd1234",
      "lightspeak://invite/AbCd1234",
      "lightspeak://invite/../../etc",
      "https://x.org/other/AbCd1234",
      "AbCd1234http://127.0.0.1:8090/invite/AbCd1234",
      "foo://x.org/invite/AbCd1234",
    ]) {
      expect(parseInvite(bad), bad).toBeNull();
    }
  });
});

describe("server info", () => {
  afterEach(() => vi.unstubAllGlobals());

  const respond = (status: number, body: unknown) =>
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify(body), { status })),
    );

  it("accepts a compatible Lightspeak server", async () => {
    respond(200, {
      product: "lightspeak",
      name: "Freunde",
      server: "0.1.0",
      protocolVersion: 1,
      signup: "closed",
    });
    expect(await fetchServerInfo("https://x.org")).toEqual({
      name: "Freunde",
      version: "0.1.0",
      signup: "closed",
    });
  });

  it("rejects other services and incompatible versions", async () => {
    respond(200, { hello: "world" });
    await expect(fetchServerInfo("https://x.org")).rejects.toMatchObject({ code: "not_lightspeak" });
    respond(404, {});
    await expect(fetchServerInfo("https://x.org")).rejects.toMatchObject({ code: "not_lightspeak" });
    respond(200, { product: "lightspeak", protocolVersion: 99 });
    await expect(fetchServerInfo("https://x.org")).rejects.toMatchObject({ code: "server_incompatible" });
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new TypeError("offline");
      }),
    );
    await expect(fetchServerInfo("https://x.org")).rejects.toMatchObject({ code: "network" });
  });

  it("falls back to the host as name and to the admin policy", async () => {
    respond(200, { product: "lightspeak", protocolVersion: 1, name: "  ", signup: "weird" });
    expect(await fetchServerInfo("https://x.org:8443")).toMatchObject({
      name: "x.org:8443",
      signup: "admin",
    });
  });
});
