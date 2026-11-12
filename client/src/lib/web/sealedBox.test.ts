import { describe, expect, it } from "vitest";
import { b64, fingerprint, generateDeviceKey, open, rawPublicKey, seal, unb64 } from "./sealedBox";

it("fingerprints include the last two public-key bytes", () => {
  const first = new Uint8Array(32).fill(17);
  const changed = first.slice();
  changed[31] = changed[31]! ^ 1;
  expect(fingerprint(changed)).not.toBe(fingerprint(first));
});

// Vectors shared with client/src-tauri/src/crypto.rs (tests::js_interop):
// secret keys are 32 × 0x11 (A) and 32 × 0x22 (B), the media key 32 × 0x42.
const PKA = "e06Qm75//kTEZaIgA31gjuNYl9Me+XLwf3SJLLD3PxM=";
const PKB = "D6poTtKIZ7l/Smot7l34zpdOdrcBjj8iocTPJnhXDyA=";
const FPA = "20162 67363 53482 80223 31257 21658";
/** Sealed by Rust (crypto_box 0.9) for A. */
const CT_RUST_FOR_A =
  "/7sf0rG6xjC0PIth8gutqKyZZ2fPlyFRRhilQpXkghVx4dT8lMVHmBSm7uC0YAMBJVV9f9RdJVjra1MEQ3gP1hS6+WxHLW1pFGkaV7jw9g0=";
const MEDIA = new Uint8Array(32).fill(0x42);

/** Imports a raw X25519 secret key (tests only; real keys are generated non-extractable). */
async function importSecret(fill: number): Promise<CryptoKey> {
  const pkcs8 = new Uint8Array([
    0x30,
    0x2e,
    0x02,
    0x01,
    0x00,
    0x30,
    0x05,
    0x06,
    0x03,
    0x2b,
    0x65,
    0x6e,
    0x04,
    0x22,
    0x04,
    0x20,
    ...new Uint8Array(32).fill(fill),
  ]);
  return crypto.subtle.importKey("pkcs8", pkcs8, { name: "X25519" }, false, ["deriveBits"]);
}

describe("sealed box (crypto_box_seal)", () => {
  it("opens an envelope sealed by the Rust client", async () => {
    const out = await open(await importSecret(0x11), unb64(PKA), unb64(CT_RUST_FOR_A));
    expect(b64(out)).toBe(b64(MEDIA));
  });

  it("rejects tampered, truncated or misaddressed envelopes", async () => {
    const sk = await importSecret(0x11);
    const ct = unb64(CT_RUST_FOR_A);
    const bad = ct.slice();
    bad[40] = bad[40]! ^ 1;
    await expect(open(sk, unb64(PKA), bad)).rejects.toThrow("envelope_invalid");
    await expect(open(sk, unb64(PKA), ct.slice(0, 47))).rejects.toThrow("envelope_invalid");
    await expect(open(await importSecret(0x22), unb64(PKB), ct)).rejects.toThrow("envelope_invalid");
  });

  it("round-trips with a fresh non-extractable device key", async () => {
    const kp = await generateDeviceKey();
    expect(kp.privateKey.extractable).toBe(false);
    const pub = await rawPublicKey(kp.publicKey);
    const ct = await seal(pub, MEDIA);
    expect(ct.length).toBe(32 + 16 + 32);
    expect(b64(await open(kp.privateKey, pub, ct))).toBe(b64(MEDIA));
    // Fresh ephemeral key every time.
    expect(b64(await seal(pub, MEDIA))).not.toBe(b64(ct));
    await expect(seal(new Uint8Array(31), MEDIA)).rejects.toThrow("public_key_invalid");
  });

  it("derives the same public key and fingerprint as Rust", async () => {
    expect(fingerprint(unb64(PKA))).toBe(FPA);
  });
});
