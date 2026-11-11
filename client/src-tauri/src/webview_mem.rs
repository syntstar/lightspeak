//! WebView2 memory target: `Low` while the window is minimised or hidden in the
//! tray, `Normal` when it is visible again (ICoreWebView2_19, WebView2 ≥ 1.0.1774).
//! Not combined with TrySuspend, as recommended by the WebView2 docs.

use std::sync::atomic::{AtomicBool, Ordering};

use tauri::{Runtime, WebviewWindow};

/// "Save memory in the background" (Settings → Performance, default on).
static SAVER: AtomicBool = AtomicBool::new(true);

pub fn set_saver(enabled: bool) {
    SAVER.store(enabled, Ordering::Relaxed);
}

pub fn set_low<R: Runtime>(window: &WebviewWindow<R>, low: bool) {
    let low = low && SAVER.load(Ordering::Relaxed);
    #[cfg(windows)]
    {
        let _ = window.with_webview(move |wv| {
            use webview2_com::Microsoft::Web::WebView2::Win32::{
                COREWEBVIEW2_MEMORY_USAGE_TARGET_LEVEL_LOW, COREWEBVIEW2_MEMORY_USAGE_TARGET_LEVEL_NORMAL,
                ICoreWebView2_19,
            };
            use windows::core::Interface;
            // SAFETY: COM calls on the UI thread (with_webview runs there).
            unsafe {
                if let Ok(core) = wv.controller().CoreWebView2()
                    && let Ok(c19) = core.cast::<ICoreWebView2_19>()
                {
                    let level = if low {
                        COREWEBVIEW2_MEMORY_USAGE_TARGET_LEVEL_LOW
                    } else {
                        COREWEBVIEW2_MEMORY_USAGE_TARGET_LEVEL_NORMAL
                    };
                    let _ = c19.SetMemoryUsageTargetLevel(level);
                }
            }
        });
    }
    #[cfg(not(windows))]
    let _ = (window, low);
}

/// Trims the working sets of this process and all of its descendants (the
/// WebView2 browser, GPU, renderer and utility processes) — what Windows does
/// on minimise. Pages that are still needed fault back in from the standby
/// list; the rest (startup leftovers) stay out. Used once after startup.
pub fn trim_process_tree() {
    #[cfg(windows)]
    // SAFETY: plain Win32 calls with owned handles that are closed below.
    unsafe {
        use windows::Win32::Foundation::CloseHandle;
        use windows::Win32::System::ProcessStatus::K32EmptyWorkingSet;
        use windows::Win32::System::Threading::{OpenProcess, PROCESS_QUERY_LIMITED_INFORMATION, PROCESS_SET_QUOTA};

        for pid in own_process_tree() {
            if let Ok(h) = OpenProcess(PROCESS_SET_QUOTA | PROCESS_QUERY_LIMITED_INFORMATION, false, pid) {
                let _ = K32EmptyWorkingSet(h);
                let _ = CloseHandle(h);
            }
        }
    }
}

/// Our process and all descendants (WebView2 browser, GPU, renderer, audio
/// service). Falls back to just our own id if the snapshot fails.
#[cfg(windows)]
pub fn own_process_tree() -> Vec<u32> {
    let Some(procs) = process_snapshot() else { return vec![std::process::id()] };
    let mut tree = vec![std::process::id()];
    let mut i = 0;
    while i < tree.len() {
        let parent = tree[i];
        let children: Vec<u32> = procs.iter().filter(|p| p.1 == parent && !tree.contains(&p.0)).map(|p| p.0).collect();
        tree.extend(children);
        i += 1;
    }
    tree
}

/// The WebView2 browser process hosting our UI, the root of everything that
/// plays sound for us. Audio loopback does not treat it as part of our own
/// tree, although Windows lists us as its parent.
#[cfg(windows)]
pub fn webview_browser_pid() -> Option<u32> {
    browser_child(&process_snapshot()?, std::process::id())
}

/// Direct WebView2 child of `parent` in a `(pid, parent, exe)` list.
fn browser_child(procs: &[(u32, u32, String)], parent: u32) -> Option<u32> {
    procs.iter().find(|p| p.1 == parent && p.2.eq_ignore_ascii_case("msedgewebview2.exe")).map(|p| p.0)
}

/// All running processes as `(pid, parent, exe)`.
#[cfg(windows)]
fn process_snapshot() -> Option<Vec<(u32, u32, String)>> {
    // SAFETY: plain Win32 calls with an owned snapshot handle closed below.
    unsafe {
        use windows::Win32::Foundation::CloseHandle;
        use windows::Win32::System::Diagnostics::ToolHelp::{
            CreateToolhelp32Snapshot, PROCESSENTRY32W, Process32FirstW, Process32NextW, TH32CS_SNAPPROCESS,
        };
        let snap = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0).ok()?;
        let mut procs = Vec::new();
        let mut e = PROCESSENTRY32W { dwSize: std::mem::size_of::<PROCESSENTRY32W>() as u32, ..Default::default() };
        if Process32FirstW(snap, &mut e).is_ok() {
            loop {
                let len = e.szExeFile.iter().position(|c| *c == 0).unwrap_or(e.szExeFile.len());
                procs.push((e.th32ProcessID, e.th32ParentProcessID, String::from_utf16_lossy(&e.szExeFile[..len])));
                if Process32NextW(snap, &mut e).is_err() {
                    break;
                }
            }
        }
        let _ = CloseHandle(snap);
        Some(procs)
    }
}

#[cfg(test)]
mod tests {
    use super::browser_child;

    #[test]
    fn finds_only_our_direct_webview_child() {
        let procs = vec![
            (10, 1, "Lightspeak.exe".to_string()),
            (20, 99, "msedgewebview2.exe".to_string()), // another app's WebView2
            (30, 10, "lightspeak-hook.exe".to_string()),
            (40, 10, "MSEdgeWebView2.exe".to_string()),
            (50, 40, "msedgewebview2.exe".to_string()), // its audio service
        ];
        assert_eq!(browser_child(&procs, 10), Some(40));
        assert_eq!(browser_child(&procs, 7), None);
    }
}
