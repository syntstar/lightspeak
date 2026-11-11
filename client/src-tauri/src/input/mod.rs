//! Global input for push-to-talk and global mute/deafen hotkeys.
//!
//! A low-level keyboard (and, only if a binding needs it, mouse) hook runs on a
//! dedicated thread **only while it is needed** (in a call, or while recording a
//! key). Nothing runs when idle. Events are never swallowed: games still receive
//! the key.

#[cfg(windows)]
mod hook;
pub mod keys;

use serde::{Deserialize, Serialize};

/// A key code: keyboard keys use their Windows virtual-key code (1..=254),
/// mouse buttons use `MOUSE_BASE + n` (n = 3 middle, 4 = X1, 5 = X2).
pub type KeyCode = u32;
pub const MOUSE_BASE: KeyCode = 0x1_0000;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum Action {
    PushToTalk,
    ToggleMute,
    ToggleDeafen,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Binding {
    pub action: Action,
    pub keys: Vec<KeyCode>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct HookConfig {
    pub bindings: Vec<Binding>,
    /// Push-to-talk release delay in milliseconds (0..=1000).
    pub release_delay_ms: u32,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase", tag = "kind")]
pub enum InputEvent {
    /// Push-to-talk state changed (already debounced by the release delay).
    Ptt { active: bool },
    /// A toggle hotkey fired (key-down edge of the whole combination).
    Toggle { action: Action },
    /// The foreground window belongs to an elevated process while we are not
    /// elevated: Windows will not deliver its input to our hook.
    ElevatedForeground { elevated: bool },
    /// Result of key recording: the full combination that was held.
    Recorded { keys: Vec<KeyCode> },
}

pub type Sink = Box<dyn Fn(InputEvent) + Send + 'static>;

#[cfg(windows)]
pub use hook::InputHook;

#[cfg(not(windows))]
pub struct InputHook;
#[cfg(not(windows))]
impl InputHook {
    pub fn start(_cfg: HookConfig, _sink: Sink) -> Result<Self, String> {
        Err("unsupported platform".into())
    }
    pub fn record(_sink: Sink) -> Result<Self, String> {
        Err("unsupported platform".into())
    }
    pub fn stop(self) {}
}

/// Normalises left/right modifier variants to the generic key so a binding
/// recorded with the left Ctrl also works with the right Ctrl.
pub fn normalize(code: KeyCode) -> KeyCode {
    match code {
        0xA0 | 0xA1 => 0x10, // L/R Shift -> Shift
        0xA2 | 0xA3 => 0x11, // L/R Ctrl -> Ctrl
        0xA4 | 0xA5 => 0x12, // L/R Alt -> Alt
        other => other,
    }
}

/// Validates a binding coming from the UI.
pub fn validate_keys(keys: &[KeyCode]) -> bool {
    !keys.is_empty()
        && keys.len() <= 4
        && keys.iter().all(|&k| {
            (1..=0xFE).contains(&k) && k != 0x01 && k != 0x02 // no plain left/right mouse via VK
                || (MOUSE_BASE + 3..=MOUSE_BASE + 5).contains(&k)
        })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalizes_modifiers() {
        assert_eq!(normalize(0xA2), 0x11);
        assert_eq!(normalize(0xA5), 0x12);
        assert_eq!(normalize(0x41), 0x41);
    }

    #[test]
    fn validates_bindings() {
        assert!(validate_keys(&[0x11, 0x41]));
        assert!(validate_keys(&[MOUSE_BASE + 4]));
        assert!(!validate_keys(&[]));
        assert!(!validate_keys(&[0x01]));
        assert!(!validate_keys(&[MOUSE_BASE + 1]));
        assert!(!validate_keys(&[1, 2, 3, 4, 5]));
    }
}
