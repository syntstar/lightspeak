//! Human-readable key names in the current keyboard layout (e.g. "STRG" on a
//! German layout). Mouse buttons are named by the UI (i18n).

use super::{KeyCode, MOUSE_BASE};

#[cfg(windows)]
pub fn key_name(code: KeyCode) -> Option<String> {
    use windows::Win32::UI::Input::KeyboardAndMouse::{GetKeyNameTextW, MAPVK_VK_TO_VSC_EX, MapVirtualKeyW};
    if code >= MOUSE_BASE || code == 0 || code > 0xFE {
        return None;
    }
    // SAFETY: pure lookups with a stack buffer.
    unsafe {
        let sc = MapVirtualKeyW(code, MAPVK_VK_TO_VSC_EX);
        if sc == 0 {
            return None;
        }
        let extended = (sc & 0xFF00) == 0xE000 || is_extended_vk(code);
        let lparam = (((sc & 0xFF) << 16) | if extended { 1 << 24 } else { 0 }) as i32;
        let mut buf = [0u16; 64];
        let n = GetKeyNameTextW(lparam, &mut buf);
        if n <= 0 {
            return None;
        }
        Some(String::from_utf16_lossy(&buf[..n as usize]))
    }
}

#[cfg(not(windows))]
pub fn key_name(_code: KeyCode) -> Option<String> {
    None
}

/// Navigation keys share scan codes with the numpad; the extended bit picks the
/// right name ("Pos1" instead of "7 (Zehnertastatur)").
#[cfg_attr(not(windows), allow(dead_code))]
fn is_extended_vk(vk: KeyCode) -> bool {
    matches!(vk, 0x21..=0x28 | 0x2D | 0x2E | 0x6F | 0x90 | 0x5B | 0x5C | 0x5D | 0xA3 | 0xA5)
}
