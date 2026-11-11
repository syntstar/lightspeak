//! E2EE device keys (X25519) and sealed-box envelopes (crypto_box, the
//! libsodium `crypto_box_seal` construction). The device secret key lives in
//! the Windows Credential Manager only; the webview never sees it.

use base64::Engine;
use base64::engine::general_purpose::STANDARD as B64;
use crypto_box::aead::OsRng;
use crypto_box::{PublicKey, SecretKey};

use crate::platform::{Profile, credentials};

const KEY_TARGET: &str = "device-key";
pub const MEDIA_KEY_LEN: usize = 32;

fn load(profile: &Profile, server: &str) -> Result<Option<SecretKey>, String> {
    let Some(raw) = credentials::read(&profile.credential_target(server, KEY_TARGET)?)? else {
        return Ok(None);
    };
    let bytes = B64.decode(raw).map_err(|_| "device_key_corrupt")?;
    SecretKey::from_slice(&bytes).map(Some).map_err(|_| "device_key_corrupt".into())
}

/// Returns the device public key (base64), creating a key pair if needed.
pub fn device_public_key(profile: &Profile, server: &str, create: bool) -> Result<Option<String>, String> {
    if let Some(sk) = load(profile, server)? {
        return Ok(Some(B64.encode(sk.public_key().as_bytes())));
    }
    if !create {
        return Ok(None);
    }
    let sk = SecretKey::generate(&mut OsRng);
    credentials::write(&profile.credential_target(server, KEY_TARGET)?, B64.encode(sk.to_bytes()).as_bytes())?;
    Ok(Some(B64.encode(sk.public_key().as_bytes())))
}

pub fn reset_device_key(profile: &Profile, server: &str) -> Result<(), String> {
    credentials::delete(&profile.credential_target(server, KEY_TARGET)?)
}

/// Opens an envelope addressed to this device and returns the media key.
pub fn open_envelope(profile: &Profile, server: &str, ciphertext_b64: &str) -> Result<String, String> {
    let sk = load(profile, server)?.ok_or("device_key_missing")?;
    let ct = B64.decode(ciphertext_b64).map_err(|_| "envelope_invalid")?;
    let key = sk.unseal(&ct).map_err(|_| "envelope_invalid")?;
    if key.len() != MEDIA_KEY_LEN {
        return Err("envelope_invalid".into());
    }
    Ok(B64.encode(key))
}

/// Seals a media key for a recipient device public key.
pub fn seal(public_key_b64: &str, media_key_b64: &str) -> Result<String, String> {
    let pk: [u8; 32] = B64.decode(public_key_b64).ok().and_then(|v| v.try_into().ok()).ok_or("public_key_invalid")?;
    let key = B64.decode(media_key_b64).map_err(|_| "media_key_invalid")?;
    if key.len() != MEDIA_KEY_LEN {
        return Err("media_key_invalid".into());
    }
    let ct = PublicKey::from(pk).seal(&mut OsRng, &key).map_err(|_| "seal_failed")?;
    Ok(B64.encode(ct))
}

/// Fresh random media key (base64).
pub fn new_media_key() -> String {
    use crypto_box::aead::rand_core::RngCore;
    let mut k = [0u8; MEDIA_KEY_LEN];
    OsRng.fill_bytes(&mut k);
    B64.encode(k)
}

/// Short fingerprint of a public key for display (TOFU / safety numbers).
pub fn fingerprint(public_key_b64: &str) -> Result<String, String> {
    use std::fmt::Write;
    let pk = B64.decode(public_key_b64).map_err(|_| "public_key_invalid")?;
    if pk.len() != 32 {
        return Err("public_key_invalid".into());
    }
    // Digits grouped like Signal safety numbers: 12 groups of 5 from the key
    // bytes, reduced mod 100000. Deterministic and readable.
    let mut out = String::new();
    for (i, chunk) in pk.chunks(5).take(6).enumerate() {
        let mut n: u64 = 0;
        for b in chunk {
            n = (n << 8) | *b as u64;
        }
        if i > 0 {
            out.push(' ');
        }
        let _ = write!(out, "{:05}", n % 100_000);
    }
    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn seal_and_open() {
        let sk = SecretKey::generate(&mut OsRng);
        let pk = B64.encode(sk.public_key().as_bytes());
        let mk = new_media_key();
        let ct = seal(&pk, &mk).unwrap();
        assert_eq!(B64.decode(&ct).unwrap().len(), 32 + 16 + 32);
        let opened = sk.unseal(&B64.decode(&ct).unwrap()).unwrap();
        assert_eq!(B64.encode(opened), mk);
        assert!(seal("AAAA", &mk).is_err());
        assert!(seal(&pk, "AAAA").is_err());
        assert_eq!(fingerprint(&pk).unwrap().len(), 6 * 5 + 5);
    }
}

/// Vectors shared with the browser client (client/src/lib/web/sealedBox.test.ts):
/// secret keys 32 × 0x11 (A) and 32 × 0x22 (B), media key 32 × 0x42.
#[cfg(test)]
mod js_interop {
    use super::*;

    const PKA: &str = "e06Qm75//kTEZaIgA31gjuNYl9Me+XLwf3SJLLD3PxM=";
    const PKB: &str = "D6poTtKIZ7l/Smot7l34zpdOdrcBjj8iocTPJnhXDyA=";
    const FPA: &str = "77310 66181 01568 90099 78335 13815";
    /// Sealed by the browser client (WebCrypto X25519 + @noble) for B.
    const CT_JS_FOR_B: &str =
        "Ogcd1c038Fc1yebP4PghH9U/UTIR8fYJapSgu74yYEgax+irYoNw8nPot+XAAoGSwDoH+RTeTdNrjzJ/3RNWGvUY8FXrLt8DDiyzpXSMrkU=";

    #[test]
    fn opens_browser_envelope_and_matches_keys() {
        let ska = SecretKey::from([0x11u8; 32]);
        let skb = SecretKey::from([0x22u8; 32]);
        assert_eq!(B64.encode(ska.public_key().as_bytes()), PKA);
        assert_eq!(B64.encode(skb.public_key().as_bytes()), PKB);
        assert_eq!(fingerprint(PKA).unwrap(), FPA);
        let opened = skb.unseal(&B64.decode(CT_JS_FOR_B).unwrap()).unwrap();
        assert_eq!(opened, vec![0x42u8; 32]);
        assert!(ska.unseal(&B64.decode(CT_JS_FOR_B).unwrap()).is_err());
    }
}
