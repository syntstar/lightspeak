import { describe, expect, it } from "vitest";
// The worker bundle exactly as Vite feeds it to the build plugin.
import worker from "../../../node_modules/livekit-client/dist/livekit-client.e2ee.worker.mjs?raw";
import { patchE2eeWorker } from "./e2eePpsPatch";

/** The patched bundle's parseRbsp + injected helper, as a callable function. */
function helper(code: string): (b: Uint8Array) => number | null {
  const rbsp = /function parseRbsp\(stream\) \{[\s\S]*?\n\}\n/.exec(code)![0];
  const inj = /\nconst MAX_PPS_ID_ENCRYPT_ATTEMPTS[\s\S]*?\n\}\n/.exec(code)![0];
  return new Function(`${rbsp}${inj}; return parseH264IdrPpsId;`)() as (b: Uint8Array) => number | null;
}

describe("patchE2eeWorker", () => {
  const patched = patchE2eeWorker(worker);

  it("adds the IDR retry to the shipped worker", () => {
    expect(patched).toContain("for (let attempt = 1;; attempt++)");
    expect(patched).toContain("let iv = this.makeIV(");
    expect(patched.match(/crypto\.subtle\.encrypt\(/g)?.length).toBe(
      worker.match(/crypto\.subtle\.encrypt\(/g)?.length,
    );
  });

  it("is idempotent and fails loudly on unknown code", () => {
    expect(patchE2eeWorker(patched)).toBe(patched);
    expect(() => patchE2eeWorker("function writeRbsp(data_in) {}")).toThrow(/livekit-client changed/);
  });

  it("reads the PPS id like a receiver (upstream vectors)", () => {
    const parse = helper(patched);
    // slice_type 7 in 7 bits: id is the first bit of the third byte.
    expect(parse(Uint8Array.from([0x65, 0x88, 0x80, 0x40, 0x87, 0x0a]))).toBe(0);
    // slice_type 2 in 3 bits: id in the second byte.
    expect(parse(Uint8Array.from([0x25, 0xb8, 0x00, 0x40, 0x00, 0x84]))).toBe(0);
    expect(parse(Uint8Array.from([0x65, 0x88, 0x5f, 0xff]))).toBe(1);
    expect(parse(Uint8Array.from([0x41, 0x9a, 0x21, 0x43]))).toBeNull();
    expect(parse(Uint8Array.from([0x65, 0x88]))).toBeNull();
  });
});
