// libsodium `crypto_box_seal` for the browser client, byte-for-byte
// compatible with the Rust side (crypto_box 0.9 `seal`/`unseal`):
//
//   epk, esk  = fresh X25519 key pair
//   nonce     = BLAKE2b-192(epk ‖ recipientPk)
//   k         = HSalsa20(X25519(esk, recipientPk), 0¹⁶)      (crypto_box_beforenm)
//   sealed    = epk ‖ XSalsa20-Poly1305_k,nonce(message)     (tag first, NaCl layout)
//
// X25519 runs in WebCrypto, so the device's private key can stay a
// non-extractable CryptoKey; only the symmetric parts use @noble (audited,
// dependency-free).
import { hsalsa, xsalsa20poly1305 } from "@noble/ciphers/salsa.js";
import { blake2b } from "@noble/hashes/blake2.js";

const X25519 = { name: "X25519" } as const;
export const KEY_LEN = 32;
export const SEAL_OVERHEAD = 32 + 16;

// "expand 32-byte k"
const SIGMA = new Uint32Array([0x61707865, 0x3320646e, 0x79622d32, 0x6b206574]);

function words(b: Uint8Array): Uint32Array {
  const v = new DataView(b.buffer, b.byteOffset, b.byteLength);
  const out = new Uint32Array(b.length / 4);
  for (let i = 0; i < out.length; i++) out[i] = v.getUint32(i * 4, true);
  return out;
}

function bytes(w: Uint32Array): Uint8Array {
  const out = new Uint8Array(w.length * 4);
  const v = new DataView(out.buffer);
  for (let i = 0; i < w.length; i++) v.setUint32(i * 4, w[i]!, true);
  return out;
}

/** crypto_box_beforenm: HSalsa20 over the raw X25519 shared secret. */
function boxKey(shared: Uint8Array): Uint8Array {
  const out = new Uint32Array(8);
  hsalsa(SIGMA, words(shared), new Uint32Array(4), out);
  const k = bytes(out);
  out.fill(0);
  return k;
}

function sealNonce(epk: Uint8Array, pk: Uint8Array): Uint8Array {
  const buf = new Uint8Array(64);
  buf.set(epk, 0);
  buf.set(pk, 32);
  return blake2b(buf, { dkLen: 24 });
}

async function sharedSecret(priv: CryptoKey, peer: Uint8Array): Promise<Uint8Array> {
  const pub = await crypto.subtle.importKey("raw", peer as BufferSource, X25519, true, []);
  // WebCrypto rejects small-order points (all-zero secret), like libsodium.
  return new Uint8Array(await crypto.subtle.deriveBits({ name: "X25519", public: pub }, priv, 256));
}

export function supported(): Promise<boolean> {
  if (typeof crypto === "undefined" || !crypto.subtle) return Promise.resolve(false);
  return crypto.subtle
    .generateKey(X25519, false, ["deriveBits"])
    .then(() => true)
    .catch(() => false);
}

/** New device key pair; the private half cannot be exported. */
export async function generateDeviceKey(): Promise<CryptoKeyPair> {
  return (await crypto.subtle.generateKey(X25519, false, ["deriveBits"])) as CryptoKeyPair;
}

export async function rawPublicKey(key: CryptoKey): Promise<Uint8Array> {
  return new Uint8Array(await crypto.subtle.exportKey("raw", key));
}

/** Seals `message` for the X25519 public key `recipient` (32 bytes). */
export async function seal(recipient: Uint8Array, message: Uint8Array): Promise<Uint8Array> {
  if (recipient.length !== KEY_LEN) throw new Error("public_key_invalid");
  const eph = (await crypto.subtle.generateKey(X25519, true, ["deriveBits"])) as CryptoKeyPair;
  const epk = await rawPublicKey(eph.publicKey);
  const shared = await sharedSecret(eph.privateKey, recipient);
  const k = boxKey(shared);
  shared.fill(0);
  try {
    const ct = xsalsa20poly1305(k, sealNonce(epk, recipient)).encrypt(message);
    const out = new Uint8Array(KEY_LEN + ct.length);
    out.set(epk, 0);
    out.set(ct, KEY_LEN);
    return out;
  } finally {
    k.fill(0);
  }
}

/** Opens a sealed box addressed to (`priv`, `pub`); throws on any mismatch. */
export async function open(priv: CryptoKey, pub: Uint8Array, sealed: Uint8Array): Promise<Uint8Array> {
  if (sealed.length < SEAL_OVERHEAD) throw new Error("envelope_invalid");
  const epk = sealed.slice(0, KEY_LEN);
  let shared: Uint8Array;
  try {
    shared = await sharedSecret(priv, epk);
  } catch {
    throw new Error("envelope_invalid");
  }
  const k = boxKey(shared);
  shared.fill(0);
  try {
    return xsalsa20poly1305(k, sealNonce(epk, pub)).decrypt(sealed.slice(KEY_LEN));
  } catch {
    throw new Error("envelope_invalid");
  } finally {
    k.fill(0);
  }
}

/** Display fingerprint, identical to `crypto::fingerprint` in Rust. */
export function fingerprint(pk: Uint8Array): string {
  if (pk.length !== KEY_LEN) throw new Error("public_key_invalid");
  const groups: string[] = [];
  for (let i = 0; i < 6; i++) {
    let n = 0;
    for (const b of pk.subarray(i * 5, i * 5 + 5)) n = n * 256 + b; // 40 bits: exact in a double
    groups.push(String(n % 100_000).padStart(5, "0"));
  }
  return groups.join(" ");
}

export function b64(b: Uint8Array): string {
  let s = "";
  for (const x of b) s += String.fromCharCode(x);
  return btoa(s);
}

export function unb64(s: string): Uint8Array {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}
