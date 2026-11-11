pub mod audio;
mod crypto;
mod diag;
pub mod input;
pub mod platform;
mod shell;
mod update;
mod webview_mem;

use std::sync::Mutex;
use std::sync::atomic::Ordering;
use tauri::{AppHandle, Emitter, Manager, State};

use platform::Profile;
use shell::Shell;

// ---------- stream audio (WASAPI process loopback) ----------

#[derive(Default)]
struct StreamAudioState(Mutex<Option<audio::loopback::LoopbackCapture>>);

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase", tag = "kind")]
enum StreamAudioSource {
    /// Track label from getDisplayMedia, e.g. `window:917540:0`.
    Window {
        label: String,
    },
    Screen,
    /// Manual pick ("Audio from: …"): must be a process that currently plays
    /// audio and is not part of our own process tree.
    Pid {
        pid: u32,
    },
}

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
struct StreamAudioInfo {
    pid: u32,
    process_name: Option<String>,
}

#[tauri::command]
fn stream_audio_start(
    app: AppHandle,
    state: State<'_, StreamAudioState>,
    source: StreamAudioSource,
    channel: tauri::ipc::Channel<tauri::ipc::InvokeResponseBody>,
) -> Result<StreamAudioInfo, String> {
    use audio::loopback::{LoopbackCapture, Target};
    use audio::process::{hwnd_from_label, process_name, window_pid};
    let (target, pid) = match source {
        StreamAudioSource::Window { label } => {
            let hwnd = hwnd_from_label(&label).ok_or("no_window")?;
            let pid = window_pid(hwnd).ok_or("no_window")?;
            if pid == std::process::id() {
                return Err("own_window".into());
            }
            (Target::Include(pid), pid)
        }
        StreamAudioSource::Screen => {
            // Our voices play in WebView2's audio service. Excluding our own
            // pid would leave them in the mix; no browser process
            // means no audio rather than sending viewers their own voices.
            let browser = webview_mem::webview_browser_pid().ok_or("no_webview")?;
            (Target::Exclude(browser), std::process::id())
        }
        StreamAudioSource::Pid { pid } => {
            let ok = audio::sessions::active_sessions()?.iter().any(|s| s.pid == pid);
            if !ok {
                return Err("no_audio_session".into());
            }
            (Target::Include(pid), pid)
        }
    };
    let mut slot = state.0.lock().map_err(|e| e.to_string())?;
    if let Some(old) = slot.take() {
        old.stop();
    }
    let ch = channel.clone();
    let cap = LoopbackCapture::start(
        target,
        move |frame| {
            let _ = ch.send(tauri::ipc::InvokeResponseBody::Raw(frame));
        },
        move |err| {
            let _ = app.emit("stream-audio-ended", err);
        },
    )?;
    *slot = Some(cap);
    Ok(StreamAudioInfo { pid, process_name: process_name(pid) })
}

/// Processes currently playing audio (fallback picker for stream audio).
#[tauri::command]
async fn audio_sessions() -> Result<Vec<audio::sessions::AudioSession>, String> {
    tauri::async_runtime::spawn_blocking(audio::sessions::active_sessions).await.map_err(|e| e.to_string())?
}

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
struct CaptureWindowInfo {
    title: String,
    process_name: Option<String>,
}

/// Caption and app of a captured window (track label `window:<hwnd>:0`), so
/// the stream dialog can say what is being shared. None for screens or
/// windows that are gone.
#[tauri::command]
fn capture_window_info(label: String) -> Option<CaptureWindowInfo> {
    use audio::process::{hwnd_from_label, process_name, window_pid, window_title};
    let hwnd = hwnd_from_label(&label)?;
    let title = window_title(hwnd)?;
    let process_name = window_pid(hwnd).and_then(process_name);
    Some(CaptureWindowInfo { title, process_name })
}

#[tauri::command]
fn stream_audio_stop(state: State<'_, StreamAudioState>) -> Result<(), String> {
    if let Some(old) = state.0.lock().map_err(|e| e.to_string())?.take() {
        old.stop();
    }
    Ok(())
}

// ---------- push-to-talk / global shortcuts ----------

#[derive(Default)]
struct InputState(Mutex<Option<input::InputHook>>);

fn sink(app: AppHandle) -> input::Sink {
    Box::new(move |ev| {
        let _ = app.emit("input", ev);
    })
}

#[tauri::command]
fn input_start(app: AppHandle, state: State<'_, InputState>, config: input::HookConfig) -> Result<(), String> {
    if !config.bindings.iter().all(|b| input::validate_keys(&b.keys)) {
        return Err("invalid_binding".into());
    }
    let mut slot = state.0.lock().map_err(|e| e.to_string())?;
    if let Some(old) = slot.take() {
        old.stop();
    }
    *slot = Some(input::InputHook::start(config, sink(app))?);
    Ok(())
}

#[tauri::command]
fn input_record(app: AppHandle, state: State<'_, InputState>) -> Result<(), String> {
    let mut slot = state.0.lock().map_err(|e| e.to_string())?;
    if let Some(old) = slot.take() {
        old.stop();
    }
    *slot = Some(input::InputHook::record(sink(app))?);
    Ok(())
}

#[tauri::command]
fn input_stop(state: State<'_, InputState>) -> Result<(), String> {
    if let Some(old) = state.0.lock().map_err(|e| e.to_string())?.take() {
        old.stop();
    }
    Ok(())
}

#[tauri::command]
fn key_name(code: u32) -> Option<String> {
    input::keys::key_name(code)
}

/// Trims the working sets of the process tree once: after a
/// call starts, WebRTC initialises the hardware video encoder in the GPU
/// process (~150 MB) even for audio-only calls; those pages are never touched
/// again and would otherwise stay resident for the whole call.
#[tauri::command]
async fn memory_trim() {
    let _ = tauri::async_runtime::spawn_blocking(webview_mem::trim_process_tree).await;
}

#[tauri::command]
fn set_memory_saver(enabled: bool) {
    webview_mem::set_saver(enabled);
}

// ---------- diagnostics ----------

#[tauri::command]
async fn process_stats(diag: State<'_, diag::Diag>) -> Result<diag::ProcStats, String> {
    Ok(diag.stats())
}

#[tauri::command]
fn log_write(diag: State<'_, diag::Diag>, level: String, message: String) {
    diag.log(&level, &message);
}

#[tauri::command]
fn logs_open(diag: State<'_, diag::Diag>) -> Result<(), String> {
    std::fs::create_dir_all(diag.dir()).map_err(|e| e.to_string())?;
    diag::reveal(diag.dir(), false);
    Ok(())
}

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
struct SystemInfo {
    windows: String,
    webview2: String,
}

#[tauri::command]
async fn system_info() -> SystemInfo {
    #[cfg(windows)]
    let windows = platform::registry::windows_version();
    #[cfg(not(windows))]
    let windows = String::new();
    SystemInfo { windows, webview2: tauri::webview_version().unwrap_or_default() }
}

/// Writes the diagnostics report (built by the UI, without secrets; scrubbed
/// again here) plus the log tail to the Downloads folder and shows it.
#[tauri::command]
async fn diagnostics_export(
    app: AppHandle,
    diag: State<'_, diag::Diag>,
    profile: State<'_, Profile>,
    report: String,
) -> Result<String, String> {
    if report.len() > 1024 * 1024 {
        return Err("too_large".into());
    }
    let dir = app.path().download_dir().unwrap_or_else(|_| profile.data_dir.clone());
    let path = diag.export(&dir, &report)?;
    diag::reveal(&path, true);
    Ok(path.display().to_string())
}

// ---------- app / profile ----------

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
struct AppInfo {
    profile: String,
    version: String,
    start_minimized: bool,
    dev: bool,
    computer_name: String,
    started_at: u64,
    updater: bool,
}

/// Process start (Unix ms) for the cold-start measurement.
static STARTED_AT: std::sync::OnceLock<u64> = std::sync::OnceLock::new();

#[tauri::command]
fn app_info(app: AppHandle, profile: State<'_, Profile>) -> AppInfo {
    AppInfo {
        profile: profile.name.clone(),
        version: app.package_info().version.to_string(),
        start_minimized: profile.start_minimized,
        dev: cfg!(debug_assertions),
        computer_name: std::env::var("COMPUTERNAME").unwrap_or_default(),
        started_at: STARTED_AT.get().copied().unwrap_or(0),
        updater: !update::PUBKEY.is_empty(),
    }
}

#[derive(Default)]
struct PendingInvite(Mutex<Option<String>>);

/// Invite link from a deep link that arrived before the UI was ready.
#[tauri::command]
fn take_pending_invite(state: State<'_, PendingInvite>) -> Option<String> {
    state.0.lock().ok()?.take()
}

#[tauri::command]
fn idle_seconds() -> u64 {
    platform::idle_seconds()
}

#[tauri::command]
fn autostart_get() -> bool {
    platform::registry::autostart_enabled()
}

#[tauri::command]
fn autostart_set(enabled: bool) -> Result<(), String> {
    platform::registry::set_autostart(enabled)
}

#[tauri::command]
fn set_close_to_tray(shell: State<'_, Shell>, enabled: bool) {
    shell.close_to_tray.store(enabled, Ordering::SeqCst);
}

#[tauri::command]
fn tray_update(app: AppHandle, state: shell::TrayState) -> Result<(), String> {
    shell::update_tray(&app, state)
}

#[tauri::command]
fn notify_show(app: AppHandle, toast: platform::toast::Toast) -> Result<(), String> {
    platform::toast::show(&app, &toast)
}

#[tauri::command]
fn notify_clear(tag: String) {
    platform::toast::clear(&tag);
}

#[tauri::command]
fn taskbar_badge(app: AppHandle, on: bool, label: String) -> Result<(), String> {
    let w = app.get_webview_window("main").ok_or("no_window")?;
    let h = w.hwnd().map_err(|e| e.to_string())?.0 as isize;
    app.run_on_main_thread(move || {
        let _ = platform::taskbar::set_overlay(h, on, &label);
    })
    .map_err(|e| e.to_string())
}

#[tauri::command]
fn window_show(app: AppHandle) {
    shell::show_main(&app);
}

#[tauri::command]
fn app_quit(app: AppHandle) {
    shell::quit(&app);
}

// ---------- local settings (non-secret, per device) ----------

const MAX_SETTINGS: usize = 256 * 1024;

#[tauri::command]
fn settings_load(shell: State<'_, Shell>) -> String {
    std::fs::read_to_string(shell.settings_path()).unwrap_or_else(|_| "{}".into())
}

#[tauri::command]
fn settings_save(shell: State<'_, Shell>, json: String) -> Result<(), String> {
    if json.len() > MAX_SETTINGS || serde_json::from_str::<serde_json::Value>(&json).is_err() {
        return Err("invalid_settings".into());
    }
    shell::write_atomic(&shell.settings_path(), json.as_bytes()).map_err(|e| e.to_string())
}

// ---------- secure store (Credential Manager) ----------

const SESSION_TARGET: &str = "session";

/// Session JSON ({server, token, userId, deviceId}) per server origin.
#[tauri::command]
fn session_get(profile: State<'_, Profile>, server: String) -> Result<Option<String>, String> {
    let raw = platform::credentials::read(&profile.credential_target(&server, SESSION_TARGET)?)?;
    Ok(raw.and_then(|b| String::from_utf8(b).ok()))
}

#[tauri::command]
fn session_set(profile: State<'_, Profile>, server: String, json: String) -> Result<(), String> {
    if serde_json::from_str::<serde_json::Value>(&json).is_err() {
        return Err("invalid_session".into());
    }
    platform::credentials::write(&profile.credential_target(&server, SESSION_TARGET)?, json.as_bytes())
}

#[tauri::command]
fn session_clear(profile: State<'_, Profile>, server: String) -> Result<(), String> {
    platform::credentials::delete(&profile.credential_target(&server, SESSION_TARGET)?)
}

// ---------- E2EE ----------

#[tauri::command]
fn device_key(profile: State<'_, Profile>, server: String, create: bool) -> Result<Option<String>, String> {
    crypto::device_public_key(&profile, &server, create)
}

#[tauri::command]
fn device_key_reset(profile: State<'_, Profile>, server: String) -> Result<(), String> {
    crypto::reset_device_key(&profile, &server)
}

#[tauri::command]
fn e2ee_open(profile: State<'_, Profile>, server: String, ciphertext: String) -> Result<String, String> {
    crypto::open_envelope(&profile, &server, &ciphertext)
}

#[tauri::command]
fn e2ee_seal(public_key: String, media_key: String) -> Result<String, String> {
    crypto::seal(&public_key, &media_key)
}

#[tauri::command]
fn e2ee_new_key() -> String {
    crypto::new_media_key()
}

#[tauri::command]
fn key_fingerprint(public_key: String) -> Result<String, String> {
    crypto::fingerprint(&public_key)
}

// ---------- entry ----------

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let _ = STARTED_AT.set(
        std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map(|d| d.as_millis() as u64).unwrap_or(0),
    );
    let profile = platform::detect_profile();
    if !profile.is_default() {
        // Each test profile gets its own WebView2 user data folder; two
        // instances cannot share one with different browser arguments.
        // SAFETY: called before any other thread is started.
        unsafe { std::env::set_var("WEBVIEW2_USER_DATA_FOLDER", profile.data_dir.join("webview")) };
    }
    let mut builder = tauri::Builder::default();
    if profile.is_default() {
        // A second start (e.g. a clicked invite link) focuses the running app
        // and forwards the link. Test profiles may run side by side.
        builder = builder.plugin(tauri_plugin_single_instance::init(|app, argv, _cwd| {
            shell::show_main(app);
            if let Some(link) = platform::invite_from_args(&argv) {
                let _ = app.emit("deep-link", link);
            }
        }));
    }
    let start_invite = platform::invite_from_args(&std::env::args().collect::<Vec<_>>());
    builder
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_updater::Builder::new().pubkey(update::PUBKEY).build())
        .manage(update::PendingUpdate::default())
        .manage(Shell::new(profile.data_dir.clone()))
        .manage(diag::Diag::new(&profile.data_dir))
        .manage(PendingInvite(Mutex::new(start_invite)))
        .manage(profile)
        .manage(InputState::default())
        .manage(StreamAudioState::default())
        .invoke_handler(tauri::generate_handler![
            app_info,
            take_pending_invite,
            idle_seconds,
            autostart_get,
            autostart_set,
            set_close_to_tray,
            tray_update,
            notify_show,
            notify_clear,
            taskbar_badge,
            window_show,
            app_quit,
            settings_load,
            settings_save,
            session_get,
            session_set,
            session_clear,
            device_key,
            device_key_reset,
            e2ee_open,
            e2ee_seal,
            e2ee_new_key,
            key_fingerprint,
            input_start,
            input_record,
            input_stop,
            key_name,
            memory_trim,
            stream_audio_start,
            stream_audio_stop,
            audio_sessions,
            capture_window_info,
            set_memory_saver,
            process_stats,
            log_write,
            logs_open,
            system_info,
            diagnostics_export,
            update::update_check,
            update::update_download,
            update::update_discard,
            update::update_install
        ])
        .setup(|app| {
            let handle = app.handle().clone();
            let profile = app.state::<Profile>().inner().clone();
            if profile.is_default()
                && let Err(e) = platform::registry::register_url_scheme()
            {
                eprintln!("url scheme registration failed: {e}");
            }
            app.state::<diag::Diag>()
                .log("info", &format!("start {} profile={}", app.package_info().version, profile.name));
            // A downloaded update is installed before anything is shown
            // (exits the process on success).
            update::install_pending_at_start(&handle);
            shell::create_tray(&handle)?;
            platform::toast::register(&profile.data_dir);
            #[cfg(windows)]
            audio::branding::start();
            if let Some(w) = app.get_webview_window("main") {
                let shell = app.state::<Shell>();
                shell::restore_geometry(&shell, &w);
                if !profile.start_minimized {
                    let _ = w.show();
                    let _ = w.set_focus();
                }
                // Startup leaves ~200 MB of transient, unneeded pages resident in
                // the WebView2 processes. A MemoryUsageTargetLevel pulse only
                // trims while the window is hidden, so once the UI has settled the
                // working sets of the process tree are trimmed once.
                let w3 = w.clone();
                std::thread::spawn(move || {
                    std::thread::sleep(std::time::Duration::from_secs(20));
                    if !w3.is_visible().unwrap_or(false) || w3.is_minimized().unwrap_or(false) {
                        webview_mem::set_low(&w3, true);
                    }
                    webview_mem::trim_process_tree();
                });
                let w2 = w.clone();
                let app2 = handle.clone();
                w.on_window_event(move |ev| match ev {
                    tauri::WindowEvent::Resized(_) => {
                        let minimized = w2.is_minimized().unwrap_or(false);
                        webview_mem::set_low(&w2, minimized);
                        if let Some(shell) = app2.try_state::<Shell>() {
                            shell::track_geometry(&shell, &w2);
                        }
                    }
                    tauri::WindowEvent::Moved(_) => {
                        if let Some(shell) = app2.try_state::<Shell>() {
                            shell::track_geometry(&shell, &w2);
                        }
                    }
                    tauri::WindowEvent::CloseRequested { api, .. } => {
                        let shell = app2.state::<Shell>();
                        if shell.close_to_tray.load(Ordering::SeqCst) && !shell.quitting.load(Ordering::SeqCst) {
                            api.prevent_close();
                            shell::hide_to_tray(&app2);
                        } else {
                            shell::track_geometry(&shell, &w2);
                            shell::save_geometry(&shell);
                        }
                    }
                    _ => {}
                });
            }
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
