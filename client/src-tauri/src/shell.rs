//! Window shell: tray icon + menu, close-to-tray, window geometry
//! persistence, local settings file.

use std::path::PathBuf;
use std::sync::Mutex;
use std::sync::atomic::{AtomicBool, Ordering};

use serde::{Deserialize, Serialize};
use tauri::menu::{CheckMenuItem, Menu, MenuItem, PredefinedMenuItem, Submenu};
use tauri::tray::{MouseButton, MouseButtonState, TrayIcon, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Emitter, Manager, PhysicalPosition, PhysicalSize, WebviewWindow, Wry};

use crate::webview_mem;

pub struct Shell {
    pub close_to_tray: AtomicBool,
    pub quitting: AtomicBool,
    geom: Mutex<Option<Geometry>>,
    tray: Mutex<Option<TrayIcon<Wry>>>,
    data_dir: PathBuf,
}

#[derive(Clone, Copy, Serialize, Deserialize, Debug)]
struct Geometry {
    x: i32,
    y: i32,
    w: u32,
    h: u32,
    maximized: bool,
}

impl Shell {
    pub fn new(data_dir: PathBuf) -> Self {
        Shell {
            close_to_tray: AtomicBool::new(true),
            quitting: AtomicBool::new(false),
            geom: Mutex::new(None),
            tray: Mutex::new(None),
            data_dir,
        }
    }

    fn geom_path(&self) -> PathBuf {
        self.data_dir.join("window.json")
    }

    pub fn settings_path(&self) -> PathBuf {
        self.data_dir.join("settings.json")
    }
}

// ---------- window geometry ----------

pub fn restore_geometry(shell: &Shell, w: &WebviewWindow) {
    let Ok(raw) = std::fs::read_to_string(shell.geom_path()) else { return };
    let Ok(g) = serde_json::from_str::<Geometry>(&raw) else { return };
    // Only restore onto a monitor that still exists.
    let on_screen = w.available_monitors().unwrap_or_default().iter().any(|m| {
        let p = m.position();
        let s = m.size();
        g.x + 40 >= p.x && g.x + 40 < p.x + s.width as i32 && g.y + 20 >= p.y && g.y + 20 < p.y + s.height as i32
    });
    if on_screen && g.w >= 600 && g.h >= 400 {
        let _ = w.set_size(PhysicalSize::new(g.w, g.h));
        let _ = w.set_position(PhysicalPosition::new(g.x, g.y));
    }
    if g.maximized {
        let _ = w.maximize();
    }
    *shell.geom.lock().unwrap() = Some(g);
}

pub fn track_geometry(shell: &Shell, w: &WebviewWindow) {
    if w.is_minimized().unwrap_or(false) {
        return;
    }
    let maximized = w.is_maximized().unwrap_or(false);
    let mut slot = shell.geom.lock().unwrap();
    if maximized {
        if let Some(g) = slot.as_mut() {
            g.maximized = true;
        }
        return;
    }
    if let (Ok(p), Ok(s)) = (w.outer_position(), w.outer_size()) {
        *slot = Some(Geometry { x: p.x, y: p.y, w: s.width, h: s.height, maximized: false });
    }
}

pub fn save_geometry(shell: &Shell) {
    if let Some(g) = *shell.geom.lock().unwrap()
        && let Ok(s) = serde_json::to_string(&g)
    {
        let _ = write_atomic(&shell.geom_path(), s.as_bytes());
    }
}

pub fn write_atomic(path: &PathBuf, data: &[u8]) -> std::io::Result<()> {
    let tmp = path.with_extension("tmp");
    std::fs::write(&tmp, data)?;
    std::fs::rename(&tmp, path)
}

// ---------- show / hide ----------

pub fn show_main(app: &AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.unminimize();
        let _ = w.show();
        let _ = w.set_focus();
        webview_mem::set_low(&w, false);
        #[cfg(windows)]
        if let Ok(hwnd) = w.hwnd() {
            let h = hwnd.0 as isize;
            let _ = app.run_on_main_thread(move || crate::platform::taskbar::reapply(h));
        }
    }
}

pub fn hide_to_tray(app: &AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        if let Some(shell) = app.try_state::<Shell>() {
            track_geometry(&shell, &w);
            save_geometry(&shell);
        }
        let _ = w.hide();
        webview_mem::set_low(&w, true);
    }
}

pub fn quit(app: &AppHandle) {
    if let Some(shell) = app.try_state::<Shell>() {
        shell.quitting.store(true, Ordering::SeqCst);
        if let Some(w) = app.get_webview_window("main") {
            track_geometry(&shell, &w);
        }
        save_geometry(&shell);
    }
    app.exit(0);
}

// ---------- tray ----------

#[derive(Deserialize, Default, Clone)]
#[serde(rename_all = "camelCase")]
pub struct TrayLabels {
    pub open: String,
    pub mute: String,
    pub deafen: String,
    pub status: String,
    pub online: String,
    pub idle: String,
    pub dnd: String,
    pub invisible: String,
    pub quit: String,
    #[serde(default)]
    pub in_voice: String,
}

/// One occupied voice channel in the tray menu ("Space · #channel: Anna, Ben").
#[derive(Deserialize, Default, Clone)]
#[serde(rename_all = "camelCase")]
pub struct TrayVoice {
    pub id: String,
    pub label: String,
    pub current: bool,
}

#[derive(Deserialize, Default, Clone)]
#[serde(rename_all = "camelCase")]
pub struct TrayState {
    pub labels: TrayLabels,
    pub tooltip: String,
    pub muted: bool,
    pub deafened: bool,
    pub status: String,
    pub unread: bool,
    pub in_call: bool,
    #[serde(default)]
    pub voice: Vec<TrayVoice>,
}

/// Upper bound for voice rows (the client sends at most this many, too).
const MAX_VOICE_ROWS: usize = 8;

fn build_menu(app: &AppHandle, st: &TrayState) -> tauri::Result<Menu<Wry>> {
    let l = &st.labels;
    let open = MenuItem::with_id(app, "open", &l.open, true, None::<&str>)?;
    let mute = CheckMenuItem::with_id(app, "mute", &l.mute, true, st.muted, None::<&str>)?;
    let deafen = CheckMenuItem::with_id(app, "deafen", &l.deafen, true, st.deafened, None::<&str>)?;
    let s_online = CheckMenuItem::with_id(app, "status:online", &l.online, true, st.status == "online", None::<&str>)?;
    let s_idle = CheckMenuItem::with_id(app, "status:idle", &l.idle, true, st.status == "idle", None::<&str>)?;
    let s_dnd = CheckMenuItem::with_id(app, "status:dnd", &l.dnd, true, st.status == "dnd", None::<&str>)?;
    let s_inv =
        CheckMenuItem::with_id(app, "status:invisible", &l.invisible, true, st.status == "invisible", None::<&str>)?;
    let status = Submenu::with_id_and_items(app, "status", &l.status, true, &[&s_online, &s_idle, &s_dnd, &s_inv])?;
    let quit = MenuItem::with_id(app, "quit", &l.quit, true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&open, &PredefinedMenuItem::separator(app)?, &mute, &deafen, &status])?;
    // Occupied voice channels: a click joins.
    if !st.voice.is_empty() {
        menu.append(&PredefinedMenuItem::separator(app)?)?;
        menu.append(&MenuItem::with_id(app, "voice-header", &l.in_voice, false, None::<&str>)?)?;
        for v in st.voice.iter().take(MAX_VOICE_ROWS) {
            let id = format!("voice:{}", v.id);
            menu.append(&CheckMenuItem::with_id(app, id, &v.label, true, v.current, None::<&str>)?)?;
        }
    }
    menu.append(&PredefinedMenuItem::separator(app)?)?;
    menu.append(&quit)?;
    Ok(menu)
}

pub fn create_tray(app: &AppHandle) -> tauri::Result<()> {
    let st = TrayState {
        labels: TrayLabels {
            open: "Open Lightspeak".into(),
            mute: "Mute".into(),
            deafen: "Deafen".into(),
            status: "Status".into(),
            online: "Online".into(),
            idle: "Away".into(),
            dnd: "Do not disturb".into(),
            invisible: "Invisible".into(),
            quit: "Quit".into(),
            in_voice: String::new(),
        },
        tooltip: "Lightspeak".into(),
        status: "online".into(),
        ..Default::default()
    };
    let menu = build_menu(app, &st)?;
    let mut b = TrayIconBuilder::with_id("main")
        .tooltip("Lightspeak")
        .menu(&menu)
        .show_menu_on_left_click(false)
        .on_menu_event(|app, ev| {
            let id = ev.id().as_ref().to_string();
            match id.as_str() {
                "open" => show_main(app),
                "quit" => quit(app),
                _ => {
                    let _ = app.emit("tray", id);
                }
            }
        })
        .on_tray_icon_event(|tray, ev| {
            if let TrayIconEvent::Click { button: MouseButton::Left, button_state: MouseButtonState::Up, .. } = ev {
                show_main(tray.app_handle());
            }
        });
    if let Some(icon) = app.default_window_icon() {
        b = b.icon(icon.clone());
    }
    let tray = b.build(app)?;
    if let Some(shell) = app.try_state::<Shell>() {
        *shell.tray.lock().unwrap() = Some(tray);
    }
    Ok(())
}

pub fn update_tray(app: &AppHandle, st: TrayState) -> Result<(), String> {
    let shell = app.try_state::<Shell>().ok_or("no_shell")?;
    let guard = shell.tray.lock().unwrap();
    let tray = guard.as_ref().ok_or("no_tray")?;
    let menu = build_menu(app, &st).map_err(|e| e.to_string())?;
    tray.set_menu(Some(menu)).map_err(|e| e.to_string())?;
    let tip = if st.tooltip.is_empty() { "Lightspeak".to_string() } else { st.tooltip.clone() };
    tray.set_tooltip(Some(tip)).map_err(|e| e.to_string())?;
    if let Some(base) = app.default_window_icon() {
        // Unread → red dot, in a call → green dot (unread wins).
        let dot = if st.unread {
            Some([255, 69, 58])
        } else if st.in_call {
            Some([48, 209, 88])
        } else {
            None
        };
        let icon = match dot {
            Some(c) => badge(base, c),
            None => base.clone().to_owned(),
        };
        tray.set_icon(Some(icon)).map_err(|e| e.to_string())?;
    }
    Ok(())
}

/// Draws a filled dot (with a thin dark ring for contrast) into the
/// bottom-right corner of an RGBA icon.
fn badge(base: &tauri::image::Image<'_>, rgb: [u8; 3]) -> tauri::image::Image<'static> {
    let (w, h) = (base.width() as i32, base.height() as i32);
    let mut px = base.rgba().to_vec();
    let r = (w.min(h) as f32 * 0.22).max(3.0);
    let (cx, cy) = (w as f32 - r - 0.5, h as f32 - r - 0.5);
    for y in 0..h {
        for x in 0..w {
            let d = ((x as f32 + 0.5 - cx).powi(2) + (y as f32 + 0.5 - cy).powi(2)).sqrt();
            let i = ((y * w + x) * 4) as usize;
            if d <= r - 1.0 {
                px[i..i + 4].copy_from_slice(&[rgb[0], rgb[1], rgb[2], 255]);
            } else if d <= r + 0.5 {
                px[i..i + 4].copy_from_slice(&[20, 20, 22, 255]);
            }
        }
    }
    tauri::image::Image::new_owned(px, w as u32, h as u32)
}
