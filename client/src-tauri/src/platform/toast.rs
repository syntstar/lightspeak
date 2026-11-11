//! Windows toast notifications (WinRT) for the unpackaged app.
//!
//! The AUMID is registered per user under
//! `HKCU\Software\Classes\AppUserModelId\<id>` (display name + icon), the
//! same mechanism the Windows App SDK uses for unpackaged apps, so toasts
//! work without a Start-menu shortcut (dev builds, portable starts).
//! Clicks arrive through `ToastNotification::Activated` while the app runs
//! (it normally lives in the tray); the launch/button arguments are emitted
//! to the UI as the `toast` event.

use std::collections::VecDeque;
use std::sync::{Mutex, OnceLock};

use tauri::{AppHandle, Emitter};
use windows::Data::Xml::Dom::XmlDocument;
use windows::Foundation::TypedEventHandler;
use windows::UI::Notifications::{ToastActivatedEventArgs, ToastNotification, ToastNotificationManager};
use windows::core::{HSTRING, IInspectable, Interface};

pub const AUMID: &str = "app.lightspeak.desktop";
const GROUP: &str = "lv";
/// Notifications we keep a reference to so their Activated handler stays
/// registered while they sit in the action center.
const KEEP: usize = 24;

static RECENT: OnceLock<Mutex<VecDeque<ToastNotification>>> = OnceLock::new();

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ToastAction {
    pub label: String,
    pub args: String,
}

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Toast {
    pub title: String,
    pub body: String,
    /// Replaces an earlier toast with the same tag (≤ 64 chars), e.g. a channel id.
    pub tag: String,
    /// Sent back on click, e.g. `open:<channelId>`.
    pub args: String,
    #[serde(default)]
    pub actions: Vec<ToastAction>,
    /// Incoming call: stays on screen until answered/declined.
    #[serde(default)]
    pub call: bool,
}

fn esc(s: &str) -> String {
    let mut o = String::with_capacity(s.len());
    for c in s.chars() {
        match c {
            '&' => o.push_str("&amp;"),
            '<' => o.push_str("&lt;"),
            '>' => o.push_str("&gt;"),
            '"' => o.push_str("&quot;"),
            '\'' => o.push_str("&apos;"),
            // XML 1.0 forbids most control characters.
            c if (c as u32) < 0x20 && c != '\n' && c != '\t' => o.push(' '),
            c => o.push(c),
        }
    }
    o
}

fn clip(s: &str, max: usize) -> String {
    if s.chars().count() <= max { s.to_string() } else { s.chars().take(max - 1).collect::<String>() + "…" }
}

pub fn xml(t: &Toast) -> String {
    let scenario = if t.call { r#" scenario="incomingCall""# } else { "" };
    let mut x = format!(
        r#"<toast launch="{}" activationType="foreground"{scenario}><visual><binding template="ToastGeneric"><text>{}</text><text>{}</text></binding></visual><audio silent="true"/>"#,
        esc(&t.args),
        esc(&clip(&t.title, 120)),
        esc(&clip(&t.body, 300)),
    );
    if !t.actions.is_empty() {
        x.push_str("<actions>");
        for a in t.actions.iter().take(5) {
            x.push_str(&format!(
                r#"<action content="{}" arguments="{}" activationType="foreground"/>"#,
                esc(&a.label),
                esc(&a.args)
            ));
        }
        x.push_str("</actions>");
    }
    x.push_str("</toast>");
    x
}

/// Registers the AUMID (idempotent) and writes the toast icon next to the
/// profile data.
pub fn register(icon_dir: &std::path::Path) {
    let icon = icon_dir.join("toast-icon.png");
    let png: &[u8] = include_bytes!("../../icons/128x128.png");
    if std::fs::metadata(&icon).map(|m| m.len() as usize != png.len()).unwrap_or(true) {
        let _ = std::fs::write(&icon, png);
    }
    if let Err(e) = super::registry::register_aumid(AUMID, "Lightspeak", &icon.to_string_lossy()) {
        eprintln!("aumid registration failed: {e}");
    }
}

pub fn show(app: &AppHandle, t: &Toast) -> Result<(), String> {
    let doc = XmlDocument::new().map_err(|e| e.to_string())?;
    doc.LoadXml(&HSTRING::from(xml(t))).map_err(|e| e.to_string())?;
    let n = ToastNotification::CreateToastNotification(&doc).map_err(|e| e.to_string())?;
    let tag = clip(&t.tag, 64);
    n.SetTag(&HSTRING::from(tag)).map_err(|e| e.to_string())?;
    n.SetGroup(&HSTRING::from(GROUP)).map_err(|e| e.to_string())?;
    let launch = t.args.clone();
    let app2 = app.clone();
    n.Activated(&TypedEventHandler::<ToastNotification, IInspectable>::new(move |_, args| {
        // Button clicks carry their own arguments; a body click the launch args.
        let a = args
            .ok()
            .ok()
            .and_then(|i| i.cast::<ToastActivatedEventArgs>().ok())
            .and_then(|x| x.Arguments().ok())
            .map(|h| h.to_string())
            .filter(|s| !s.is_empty())
            .unwrap_or_else(|| launch.clone());
        crate::shell::show_main(&app2);
        let _ = app2.emit("toast", a);
        Ok(())
    }))
    .map_err(|e| e.to_string())?;
    let notifier =
        ToastNotificationManager::CreateToastNotifierWithId(&HSTRING::from(AUMID)).map_err(|e| e.to_string())?;
    notifier.Show(&n).map_err(|e| e.to_string())?;
    let mut q = RECENT.get_or_init(|| Mutex::new(VecDeque::new())).lock().unwrap();
    q.push_back(n);
    while q.len() > KEEP {
        q.pop_front();
    }
    Ok(())
}

/// Removes toasts with this tag from screen and action center.
pub fn clear(tag: &str) {
    if let Ok(h) = ToastNotificationManager::History() {
        let _ = h.RemoveGroupedTagWithId(&HSTRING::from(clip(tag, 64)), &HSTRING::from(GROUP), &HSTRING::from(AUMID));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn xml_is_escaped() {
        let t = Toast {
            title: "A <b>&\"".into(),
            body: "x\u{1}y".into(),
            tag: "1".into(),
            args: "open:1\"/><x".into(),
            actions: vec![ToastAction { label: "Ok".into(), args: "a&b".into() }],
            call: true,
        };
        let x = xml(&t);
        assert!(x.contains("A &lt;b&gt;&amp;&quot;"));
        assert!(x.contains("x y"));
        assert!(x.contains(r#"launch="open:1&quot;/&gt;&lt;x""#));
        assert!(x.contains(r#"arguments="a&amp;b""#));
        assert!(x.contains(r#"scenario="incomingCall""#));
    }
}
