//! Per-user registry entries: autostart (Run key) and the `lightspeak://`
//! URL scheme. Only HKEY_CURRENT_USER is touched; no admin rights needed.

use windows::Win32::Foundation::ERROR_SUCCESS;
use windows::Win32::System::Registry::{
    HKEY_CURRENT_USER, REG_SZ, RRF_RT_REG_SZ, RegDeleteKeyValueW, RegDeleteTreeW, RegGetValueW, RegSetKeyValueW,
};
use windows::core::{HSTRING, PCWSTR};

const RUN_KEY: &str = r"Software\Microsoft\Windows\CurrentVersion\Run";
const RUN_VALUE: &str = "Lightspeak";
const SCHEME_KEY: &str = r"Software\Classes\lightspeak";

fn exe() -> Result<String, String> {
    std::env::current_exe().map(|p| p.to_string_lossy().into_owned()).map_err(|e| e.to_string())
}

fn set_sz(key: &str, name: Option<&str>, value: &str) -> Result<(), String> {
    let data: Vec<u16> = value.encode_utf16().chain(std::iter::once(0)).collect();
    let name_h = name.map(HSTRING::from);
    let name_p = name_h.as_ref().map(|h| PCWSTR(h.as_ptr())).unwrap_or(PCWSTR::null());
    // SAFETY: data outlives the call; size in bytes includes the terminator.
    let rc = unsafe {
        RegSetKeyValueW(
            HKEY_CURRENT_USER,
            &HSTRING::from(key),
            name_p,
            REG_SZ.0,
            Some(data.as_ptr() as *const _),
            (data.len() * 2) as u32,
        )
    };
    if rc == ERROR_SUCCESS { Ok(()) } else { Err(format!("registry_write: {}", rc.0)) }
}

fn get_sz(key: &str, name: Option<&str>) -> Option<String> {
    let name_h = name.map(HSTRING::from);
    let name_p = name_h.as_ref().map(|h| PCWSTR(h.as_ptr())).unwrap_or(PCWSTR::null());
    let mut buf = vec![0u16; 1024];
    let mut size = (buf.len() * 2) as u32;
    // SAFETY: buffer and size describe the same allocation.
    let rc = unsafe {
        RegGetValueW(
            HKEY_CURRENT_USER,
            &HSTRING::from(key),
            name_p,
            RRF_RT_REG_SZ,
            None,
            Some(buf.as_mut_ptr() as *mut _),
            Some(&mut size),
        )
    };
    if rc != ERROR_SUCCESS {
        return None;
    }
    let len = (size as usize / 2).saturating_sub(1);
    Some(String::from_utf16_lossy(&buf[..len]))
}

pub fn autostart_enabled() -> bool {
    match (get_sz(RUN_KEY, Some(RUN_VALUE)), exe()) {
        (Some(v), Ok(e)) => v.contains(&e),
        _ => false,
    }
}

pub fn set_autostart(enabled: bool) -> Result<(), String> {
    if enabled {
        set_sz(RUN_KEY, Some(RUN_VALUE), &format!("\"{}\" --minimized", exe()?))
    } else {
        // SAFETY: plain call; missing value is fine.
        unsafe {
            let _ = RegDeleteKeyValueW(HKEY_CURRENT_USER, &HSTRING::from(RUN_KEY), &HSTRING::from(RUN_VALUE));
        }
        Ok(())
    }
}

/// Registers `lightspeak://` for the current executable (idempotent). The
/// installer does the same; this keeps portable/dev builds working.
pub fn register_url_scheme() -> Result<(), String> {
    let cmd = format!("\"{}\" \"%1\"", exe()?);
    if get_sz(&format!(r"{SCHEME_KEY}\shell\open\command"), None).as_deref() == Some(cmd.as_str()) {
        return Ok(());
    }
    set_sz(SCHEME_KEY, None, "URL:Lightspeak")?;
    set_sz(SCHEME_KEY, Some("URL Protocol"), "")?;
    set_sz(&format!(r"{SCHEME_KEY}\DefaultIcon"), None, &format!("\"{}\",0", exe()?))?;
    set_sz(&format!(r"{SCHEME_KEY}\shell\open\command"), None, &cmd)
}

/// Registers an AppUserModelID for toasts of the unpackaged app (idempotent).
pub fn register_aumid(aumid: &str, display_name: &str, icon_path: &str) -> Result<(), String> {
    let key = format!(r"Software\Classes\AppUserModelId\{aumid}");
    if get_sz(&key, Some("IconUri")).as_deref() == Some(icon_path)
        && get_sz(&key, Some("DisplayName")).as_deref() == Some(display_name)
    {
        return Ok(());
    }
    set_sz(&key, Some("DisplayName"), display_name)?;
    set_sz(&key, Some("IconUri"), icon_path)
}

#[allow(dead_code)]
pub fn unregister_url_scheme() {
    // SAFETY: plain call on our own key.
    unsafe {
        let _ = RegDeleteTreeW(HKEY_CURRENT_USER, &HSTRING::from(SCHEME_KEY));
    }
}

/// Windows version for diagnostics, e.g. "Windows 11 24H2 (26100.2314)".
pub fn windows_version() -> String {
    use windows::Win32::System::Registry::{HKEY_LOCAL_MACHINE, RRF_RT_REG_DWORD};
    const KEY: &str = r"SOFTWARE\Microsoft\Windows NT\CurrentVersion";
    let sz = |name: &str| -> Option<String> {
        let mut buf = vec![0u16; 256];
        let mut size = (buf.len() * 2) as u32;
        // SAFETY: buffer and size describe the same allocation.
        let rc = unsafe {
            RegGetValueW(
                HKEY_LOCAL_MACHINE,
                &HSTRING::from(KEY),
                &HSTRING::from(name),
                RRF_RT_REG_SZ,
                None,
                Some(buf.as_mut_ptr() as *mut _),
                Some(&mut size),
            )
        };
        (rc == ERROR_SUCCESS).then(|| String::from_utf16_lossy(&buf[..(size as usize / 2).saturating_sub(1)]))
    };
    let mut ubr = 0u32;
    let mut size = 4u32;
    // SAFETY: ubr is a 4-byte out buffer.
    let rc = unsafe {
        RegGetValueW(
            HKEY_LOCAL_MACHINE,
            &HSTRING::from(KEY),
            &HSTRING::from("UBR"),
            RRF_RT_REG_DWORD,
            None,
            Some(&mut ubr as *mut u32 as *mut _),
            Some(&mut size),
        )
    };
    let build = sz("CurrentBuild").unwrap_or_default();
    let major = if build.parse::<u32>().unwrap_or(0) >= 22000 { "11" } else { "10" };
    let display = sz("DisplayVersion").unwrap_or_default();
    let ubr = if rc == ERROR_SUCCESS { format!(".{ubr}") } else { String::new() };
    format!("Windows {major} {display} ({build}{ubr})").replace("  ", " ")
}
