//! Maps a captured window (HWND from the getDisplayMedia track label
//! `window:<hwnd>:0`) to its owning process.

use windows::Win32::Foundation::{CloseHandle, HWND};
use windows::Win32::System::Threading::{
    OpenProcess, PROCESS_NAME_WIN32, PROCESS_QUERY_LIMITED_INFORMATION, QueryFullProcessImageNameW,
};
use windows::Win32::UI::WindowsAndMessaging::{GetWindowTextW, GetWindowThreadProcessId, IsWindow};
use windows::core::PWSTR;

/// Caption of a top-level window (empty if it has none). For windows of other
/// processes Windows returns the cached caption without messaging the window,
/// so a hung app cannot block us.
pub fn window_title(hwnd: isize) -> Option<String> {
    let h = HWND(hwnd as *mut core::ffi::c_void);
    // SAFETY: plain queries on a (possibly stale) window handle; buffer sized correctly.
    unsafe {
        if !IsWindow(Some(h)).as_bool() {
            return None;
        }
        let mut buf = [0u16; 256];
        let n = GetWindowTextW(h, &mut buf);
        Some(String::from_utf16_lossy(&buf[..n.max(0) as usize]))
    }
}

/// Parses a Chromium desktop media id such as `window:917540:0`.
pub fn hwnd_from_label(label: &str) -> Option<isize> {
    let mut it = label.split(':');
    if it.next()? != "window" {
        return None;
    }
    it.next()?.parse::<isize>().ok().filter(|h| *h != 0)
}

pub fn window_pid(hwnd: isize) -> Option<u32> {
    let h = HWND(hwnd as *mut core::ffi::c_void);
    // SAFETY: plain queries on a (possibly stale) window handle.
    unsafe {
        if !IsWindow(Some(h)).as_bool() {
            return None;
        }
        let mut pid = 0u32;
        GetWindowThreadProcessId(h, Some(&mut pid));
        (pid != 0).then_some(pid)
    }
}

/// Executable file name (e.g. `chrome.exe`) for display in the UI.
pub fn process_name(pid: u32) -> Option<String> {
    // SAFETY: handle opened with minimal rights and closed; buffer sized correctly.
    unsafe {
        let h = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, pid).ok()?;
        let mut buf = [0u16; 512];
        let mut len = buf.len() as u32;
        let ok = QueryFullProcessImageNameW(h, PROCESS_NAME_WIN32, PWSTR(buf.as_mut_ptr()), &mut len).is_ok();
        let _ = CloseHandle(h);
        if !ok {
            return None;
        }
        let path = String::from_utf16_lossy(&buf[..len as usize]);
        path.rsplit(['\\', '/']).next().map(str::to_owned)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn parses_labels() {
        assert_eq!(hwnd_from_label("window:917540:0"), Some(917540));
        assert_eq!(hwnd_from_label("screen:1:0"), None);
        assert_eq!(hwnd_from_label("window:abc:0"), None);
        assert_eq!(hwnd_from_label(""), None);
    }
}
