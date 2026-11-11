//! Names our audio in the Windows volume mixer "Lightspeak" with the app icon
//!. WebView2 plays all sound from its own audio service process
//! (msedgewebview2.exe), so without this the mixer shows "Microsoft Edge
//! WebView2". Windows lets us set name and icon of that child's sessions.
//!
//! Event-driven, idle costs nothing: one thread waits on a channel; Windows
//! wakes it when a session is created (IAudioSessionNotification) or an
//! output device comes or goes (IMMNotificationClient).

use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::Duration;

use windows::Win32::Foundation::PROPERTYKEY;
use windows::Win32::Media::Audio::{
    DEVICE_STATE, DEVICE_STATE_ACTIVE, EDataFlow, ERole, IAudioSessionControl, IAudioSessionControl2,
    IAudioSessionManager2, IAudioSessionNotification, IAudioSessionNotification_Impl, IMMDeviceEnumerator,
    IMMNotificationClient, IMMNotificationClient_Impl, MMDeviceEnumerator, eRender,
};
use windows::Win32::System::Com::{CLSCTX_ALL, COINIT_MULTITHREADED, CoCreateInstance, CoInitializeEx, CoTaskMemFree};
use windows::core::{HSTRING, Interface, PCWSTR, Ref, Result, implement};

const NAME: &str = "Lightspeak";

enum Msg {
    /// A session was created somewhere: name ours.
    Sessions,
    /// Output devices changed: register on the current set.
    Devices,
}

#[implement(IAudioSessionNotification)]
struct SessionWatch(Sender<Msg>);

impl IAudioSessionNotification_Impl for SessionWatch_Impl {
    fn OnSessionCreated(&self, _s: Ref<IAudioSessionControl>) -> Result<()> {
        // No work inside the callback (audio service thread): hand it over.
        let _ = self.0.send(Msg::Sessions);
        Ok(())
    }
}

#[implement(IMMNotificationClient)]
struct DeviceWatch(Sender<Msg>);

impl IMMNotificationClient_Impl for DeviceWatch_Impl {
    fn OnDeviceStateChanged(&self, _id: &PCWSTR, _state: DEVICE_STATE) -> Result<()> {
        let _ = self.0.send(Msg::Devices);
        Ok(())
    }
    fn OnDeviceAdded(&self, _id: &PCWSTR) -> Result<()> {
        let _ = self.0.send(Msg::Devices);
        Ok(())
    }
    fn OnDeviceRemoved(&self, _id: &PCWSTR) -> Result<()> {
        let _ = self.0.send(Msg::Devices);
        Ok(())
    }
    fn OnDefaultDeviceChanged(&self, _flow: EDataFlow, _role: ERole, _id: &PCWSTR) -> Result<()> {
        Ok(())
    }
    fn OnPropertyValueChanged(&self, _id: &PCWSTR, _key: &PROPERTYKEY) -> Result<()> {
        Ok(())
    }
}

/// Starts the watcher thread (once, at app start). Failures only cost the name.
pub fn start() {
    let _ = std::thread::Builder::new().name("audio-branding".into()).spawn(|| {
        if let Err(e) = run() {
            eprintln!("audio branding: {e}");
        }
    });
}

fn run() -> Result<()> {
    // SAFETY: COM is initialized for this thread before any COM call; all
    // interfaces live on this thread until it ends with the process.
    unsafe {
        CoInitializeEx(None, COINIT_MULTITHREADED).ok()?;
        let devices: IMMDeviceEnumerator = CoCreateInstance(&MMDeviceEnumerator, None, CLSCTX_ALL)?;
        let (tx, rx) = channel();
        let client: IMMNotificationClient = DeviceWatch(tx.clone()).into();
        devices.RegisterEndpointNotificationCallback(&client)?;
        let watch: IAudioSessionNotification = SessionWatch(tx).into();
        let icon = std::env::current_exe().map(|p| format!("{},0", p.display())).unwrap_or_default();
        let mut managers = register(&devices, &watch);
        brand(&managers, &icon);
        loop {
            let Ok(first) = rx.recv() else { return Ok(()) };
            let devices_changed = drain(&rx, matches!(first, Msg::Devices));
            if devices_changed {
                for m in &managers {
                    let _ = m.UnregisterSessionNotification(&watch);
                }
                managers = register(&devices, &watch);
            }
            brand(&managers, &icon);
        }
    }
}

/// Collects the burst of events (a call opens several streams), then reports
/// whether any of them was a device change.
fn drain(rx: &Receiver<Msg>, mut devices: bool) -> bool {
    std::thread::sleep(Duration::from_millis(300));
    while let Ok(m) = rx.try_recv() {
        devices |= matches!(m, Msg::Devices);
    }
    devices
}

/// Session managers of all active output devices, each watching for new sessions.
unsafe fn register(devices: &IMMDeviceEnumerator, watch: &IAudioSessionNotification) -> Vec<IAudioSessionManager2> {
    let mut out = Vec::new();
    // SAFETY: COM calls on interfaces owned by this thread.
    unsafe {
        let Ok(list) = devices.EnumAudioEndpoints(eRender, DEVICE_STATE_ACTIVE) else { return out };
        for i in 0..list.GetCount().unwrap_or(0) {
            let Ok(dev) = list.Item(i) else { continue };
            let Ok(m) = dev.Activate::<IAudioSessionManager2>(CLSCTX_ALL, None) else { continue };
            if m.RegisterSessionNotification(watch).is_ok() {
                out.push(m);
            }
        }
    }
    out
}

/// Names every session of our process tree that is not named yet.
unsafe fn brand(managers: &[IAudioSessionManager2], icon: &str) {
    let own = crate::webview_mem::own_process_tree();
    let name = HSTRING::from(NAME);
    let icon = HSTRING::from(icon);
    // SAFETY: COM calls on interfaces owned by this thread. Enumerating also
    // keeps the notifications coming (they start only after a first enumeration).
    unsafe {
        for m in managers {
            let Ok(list) = m.GetSessionEnumerator() else { continue };
            for i in 0..list.GetCount().unwrap_or(0) {
                let Ok(s) = list.GetSession(i) else { continue };
                let Ok(s2) = s.cast::<IAudioSessionControl2>() else { continue };
                let Ok(pid) = s2.GetProcessId() else { continue };
                if pid == 0 || !own.contains(&pid) {
                    continue;
                }
                if let Ok(p) = s.GetDisplayName() {
                    let named = p.to_string().is_ok_and(|n| n == NAME);
                    CoTaskMemFree(Some(p.0 as *const _));
                    if named {
                        continue;
                    }
                }
                let _ = s.SetDisplayName(&name, std::ptr::null());
                if !icon.is_empty() {
                    let _ = s.SetIconPath(&icon, std::ptr::null());
                }
            }
        }
    }
}
