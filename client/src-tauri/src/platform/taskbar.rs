//! Taskbar overlay badge (red dot over the app icon) via ITaskbarList3.
//! Must run on the UI thread (COM is initialised there by the webview).
//! The taskbar button disappears while the window is hidden in the tray,
//! so the last state is re-applied whenever the window is shown again.

use std::sync::Mutex;
use std::sync::atomic::{AtomicBool, Ordering};

use windows::Win32::Foundation::HWND;
use windows::Win32::Graphics::Gdi::{CreateBitmap, DeleteObject, HGDIOBJ};
use windows::Win32::System::Com::{CLSCTX_INPROC_SERVER, CoCreateInstance};
use windows::Win32::UI::Shell::{ITaskbarList3, TaskbarList};
use windows::Win32::UI::WindowsAndMessaging::{CreateIconIndirect, DestroyIcon, HICON, ICONINFO};
use windows::core::{HSTRING, PCWSTR};

static BADGE: AtomicBool = AtomicBool::new(false);
static LABEL: Mutex<String> = Mutex::new(String::new());

const SIZE: i32 = 16;

/// 16×16 BGRA dot (Apple red) with a soft edge.
fn dot_pixels() -> Vec<u8> {
    let mut px = vec![0u8; (SIZE * SIZE * 4) as usize];
    let c = SIZE as f32 / 2.0;
    let r = c - 1.0;
    for y in 0..SIZE {
        for x in 0..SIZE {
            let d = ((x as f32 + 0.5 - c).powi(2) + (y as f32 + 0.5 - c).powi(2)).sqrt();
            let a = (r + 0.5 - d).clamp(0.0, 1.0);
            let i = ((y * SIZE + x) * 4) as usize;
            // Premultiplied BGRA of #FF453A.
            px[i] = (0x3A as f32 * a) as u8;
            px[i + 1] = (0x45 as f32 * a) as u8;
            px[i + 2] = (0xFF as f32 * a) as u8;
            px[i + 3] = (255.0 * a) as u8;
        }
    }
    px
}

fn make_icon() -> Option<HICON> {
    let px = dot_pixels();
    // SAFETY: bitmaps are created from owned buffers and deleted after the
    // icon has copied them.
    unsafe {
        let color = CreateBitmap(SIZE, SIZE, 1, 32, Some(px.as_ptr() as *const _));
        let mask = CreateBitmap(SIZE, SIZE, 1, 1, None);
        let info = ICONINFO { fIcon: true.into(), xHotspot: 0, yHotspot: 0, hbmMask: mask, hbmColor: color };
        let icon = CreateIconIndirect(&info).ok();
        let _ = DeleteObject(HGDIOBJ(color.0));
        let _ = DeleteObject(HGDIOBJ(mask.0));
        icon
    }
}

/// Sets or clears the overlay; `label` is the accessible description.
/// `hwnd` is passed as an integer so callers can move it to the UI thread.
pub fn set_overlay(hwnd: isize, on: bool, label: &str) -> Result<(), String> {
    BADGE.store(on, Ordering::SeqCst);
    *LABEL.lock().unwrap() = label.to_string();
    apply(HWND(hwnd as *mut _), on, label)
}

/// Re-applies the last state (after the window came back from the tray).
pub fn reapply(hwnd: isize) {
    if BADGE.load(Ordering::SeqCst) {
        let label = LABEL.lock().unwrap().clone();
        let _ = apply(HWND(hwnd as *mut _), true, &label);
    }
}

fn apply(hwnd: HWND, on: bool, label: &str) -> Result<(), String> {
    // SAFETY: COM call on the UI thread; the icon is destroyed after the
    // taskbar has made its own copy.
    unsafe {
        let tb: ITaskbarList3 =
            CoCreateInstance(&TaskbarList, None, CLSCTX_INPROC_SERVER).map_err(|e| e.to_string())?;
        tb.HrInit().map_err(|e| e.to_string())?;
        if on {
            let icon = make_icon().ok_or("icon")?;
            let desc = HSTRING::from(label);
            let r = tb.SetOverlayIcon(hwnd, icon, PCWSTR(desc.as_ptr()));
            let _ = DestroyIcon(icon);
            r.map_err(|e| e.to_string())
        } else {
            tb.SetOverlayIcon(hwnd, HICON::default(), PCWSTR::null()).map_err(|e| e.to_string())
        }
    }
}
