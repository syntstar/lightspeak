//! Lists processes that currently play audio on the default output device.
//! Used as a fallback picker ("Audio from: …") when a window cannot be mapped
//! to the process that actually renders its sound.

use serde::Serialize;

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AudioSession {
    pub pid: u32,
    pub process_name: Option<String>,
    pub peak: f32,
}

pub fn active_sessions() -> Result<Vec<AudioSession>, String> {
    use wasapi::{DeviceEnumerator, Direction, SessionState, initialize_mta};
    let _ = initialize_mta();
    let e = |x: wasapi::WasapiError| x.to_string();
    let dev = DeviceEnumerator::new().map_err(e)?.get_default_device(&Direction::Render).map_err(e)?;
    let sessions = dev.get_iaudiosessionmanager().map_err(e)?.get_audiosessionenumerator().map_err(e)?;
    let mut out: Vec<AudioSession> = Vec::new();
    // Never offer ourselves (WebView2 renders the call audio in a child
    // process): capturing it would send the others their own voices back.
    let own = crate::webview_mem::own_process_tree();
    for i in 0..sessions.get_count().map_err(e)? {
        let Ok(ctrl) = sessions.get_session(i) else { continue };
        if ctrl.get_state().ok() != Some(SessionState::Active) {
            continue;
        }
        let Ok(pid) = ctrl.get_process_id() else { continue };
        if pid == 0 || own.contains(&pid) {
            continue;
        }
        let peak = ctrl.get_audiometerinformation().and_then(|m| m.get_peak_value()).unwrap_or(0.0);
        if let Some(s) = out.iter_mut().find(|s| s.pid == pid) {
            s.peak = s.peak.max(peak);
        } else {
            out.push(AudioSession { pid, process_name: super::process::process_name(pid), peak });
        }
    }
    Ok(out)
}
