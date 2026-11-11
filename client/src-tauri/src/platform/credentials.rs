//! Windows Credential Manager (generic credentials, local persistence).
//! Secrets never touch disk in plain text and never reach webview storage.

use windows::Win32::Foundation::ERROR_NOT_FOUND;
use windows::Win32::Security::Credentials::{
    CRED_PERSIST_LOCAL_MACHINE, CRED_TYPE_GENERIC, CREDENTIALW, CredDeleteW, CredFree, CredReadW, CredWriteW,
};
use windows::core::{HSTRING, PWSTR};

const MAX_BLOB: usize = 2560; // CRED_MAX_CREDENTIAL_BLOB_SIZE

pub fn write(target: &str, secret: &[u8]) -> Result<(), String> {
    if secret.len() > MAX_BLOB {
        return Err("secret_too_large".into());
    }
    let mut name: Vec<u16> = target.encode_utf16().chain(std::iter::once(0)).collect();
    let mut user: Vec<u16> = "Lightspeak".encode_utf16().chain(std::iter::once(0)).collect();
    let mut blob = secret.to_vec();
    let cred = CREDENTIALW {
        Type: CRED_TYPE_GENERIC,
        TargetName: PWSTR(name.as_mut_ptr()),
        CredentialBlobSize: blob.len() as u32,
        CredentialBlob: blob.as_mut_ptr(),
        Persist: CRED_PERSIST_LOCAL_MACHINE,
        UserName: PWSTR(user.as_mut_ptr()),
        ..Default::default()
    };
    // SAFETY: all pointers stay valid for the duration of the call.
    let res = unsafe { CredWriteW(&cred, 0) };
    zero(&mut blob);
    res.map_err(|e| format!("credential_write: {e}"))
}

pub fn read(target: &str) -> Result<Option<Vec<u8>>, String> {
    let mut ptr: *mut CREDENTIALW = std::ptr::null_mut();
    // SAFETY: CredReadW allocates `ptr`; we copy the blob and free it.
    unsafe {
        match CredReadW(&HSTRING::from(target), CRED_TYPE_GENERIC, None, &mut ptr) {
            Ok(()) => {
                let c = &*ptr;
                let out = std::slice::from_raw_parts(c.CredentialBlob, c.CredentialBlobSize as usize).to_vec();
                CredFree(ptr as *const _);
                Ok(Some(out))
            }
            Err(e) if e.code() == ERROR_NOT_FOUND.to_hresult() => Ok(None),
            Err(e) => Err(format!("credential_read: {e}")),
        }
    }
}

pub fn delete(target: &str) -> Result<(), String> {
    // SAFETY: plain call with an owned wide string.
    match unsafe { CredDeleteW(&HSTRING::from(target), CRED_TYPE_GENERIC, None) } {
        Ok(()) => Ok(()),
        Err(e) if e.code() == ERROR_NOT_FOUND.to_hresult() => Ok(()),
        Err(e) => Err(format!("credential_delete: {e}")),
    }
}

fn zero(b: &mut [u8]) {
    for x in b.iter_mut() {
        // SAFETY: volatile write to an owned byte keeps the compiler from
        // eliding the wipe.
        unsafe { std::ptr::write_volatile(x, 0) };
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn roundtrip() {
        let t = format!("Lightspeak/unittest/{}", std::process::id());
        super::write(&t, b"hello").unwrap();
        assert_eq!(super::read(&t).unwrap().as_deref(), Some(&b"hello"[..]));
        super::delete(&t).unwrap();
        assert_eq!(super::read(&t).unwrap(), None);
        super::delete(&t).unwrap();
    }
}
