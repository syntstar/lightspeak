//! Client self-update (Tauri updater).
//!
//! The public key is compiled in (`build.rs` reads `updater.pub`); without it
//! the build has no updater and the UI says so. Updates come from the GitHub
//! Releases of the project (`DEFAULT_URL`), independent of the server the
//! user is connected to. A fork sets its own source at build time with
//! `LIGHTSPEAK_UPDATE_URL` (and ships its own `updater.pub`).
//! Trust rests on the signature alone, not on the host: the artifact must be
//! signed by the release key and – `requireSignedVersion` in tauri.conf.json –
//! the signed version must match the announced one, so a manifest cannot pass
//! off an older signed release as new.
//!
//! Automatic updates: the installer is downloaded in the
//! background (verified) into `<profile>/update/` and installed at the next
//! start, before the window appears - never during a call. At that point a
//! fresh manifest is fetched (short timeout) and the cached bytes are
//! verified again against its signature, so a swapped file on disk is never
//! run. Offline at start: normal start, retried next time.

use serde::{Deserialize, Serialize};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tauri::{AppHandle, Emitter, Manager, State, Url};
use tauri_plugin_updater::{Update, Updater, UpdaterExt};

use crate::platform::Profile;
use crate::shell::write_atomic;

/// Minisign public key (base64 of the .pub file), empty when not configured.
pub const PUBKEY: &str = match option_env!("LIGHTSPEAK_UPDATER_PUBKEY") {
    Some(k) => k,
    None => "",
};
/// Update manifest of the official builds.
const DEFAULT_URL: &str = "https://github.com/syntstar/lightspeak/releases/latest/download/latest.json";
const UPDATE_URL: &str = match option_env!("LIGHTSPEAK_UPDATE_URL") {
    Some(u) => u,
    None => DEFAULT_URL,
};

#[derive(Default)]
pub struct PendingUpdate(Mutex<Option<Update>>);

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct UpdateInfo {
    version: String,
    notes: Option<String>,
    date: Option<String>,
}

#[derive(Serialize, Clone)]
struct Progress {
    done: u64,
    total: Option<u64>,
}

fn endpoint() -> Result<Url, String> {
    let url = Url::parse(UPDATE_URL).map_err(|_| "bad_update_url".to_string())?;
    if url.scheme() != "https" {
        return Err("bad_update_url".into());
    }
    Ok(url)
}

/// A downloaded, verified installer waiting for the next start.
#[derive(Serialize, Deserialize)]
struct Cached {
    version: String,
}

const CACHE_EXE: &str = "setup.exe";
const CACHE_META: &str = "pending.json";
/// Upper bound for the manifest request before the window shows.
const START_TIMEOUT: Duration = Duration::from_secs(4);
/// Hard cap for the whole step (TLS/proxy setup, antivirus scan of the
/// cached file …); after it the app starts normally, the update stays pending.
const START_BUDGET: Duration = Duration::from_secs(5);

fn cache_dir(app: &AppHandle) -> PathBuf {
    app.state::<Profile>().data_dir.join("update")
}

fn cached_meta(dir: &Path) -> Option<Cached> {
    serde_json::from_str(&std::fs::read_to_string(dir.join(CACHE_META)).ok()?).ok()
}

fn discard(dir: &Path) {
    let _ = std::fs::remove_dir_all(dir);
}

fn updater(app: &AppHandle, timeout: Duration) -> Result<Updater, String> {
    if PUBKEY.is_empty() {
        return Err("not_configured".into());
    }
    let url = endpoint()?;
    app.updater_builder()
        .endpoints(vec![url])
        .map_err(|e| e.to_string())?
        .timeout(timeout)
        .build()
        .map_err(|e| e.to_string())
}

fn log(app: &AppHandle, level: &str, msg: &str) {
    if let Some(d) = app.try_state::<crate::diag::Diag>() {
        d.log(level, msg);
    }
}

/// Called during setup, before the window is shown. Returns only when there
/// is nothing to install (or installing failed); on success the installer
/// takes over, the process exits and the new version starts afterwards.
pub fn install_pending_at_start(app: &AppHandle) {
    let dir = cache_dir(app);
    if !dir.exists() {
        return;
    }
    let Some(meta) = cached_meta(&dir) else {
        discard(&dir); // interrupted download
        return;
    };
    if PUBKEY.is_empty() || meta.version == app.package_info().version.to_string() {
        discard(&dir); // already installed
        return;
    }
    let app2 = app.clone();
    let abandoned = Arc::new(AtomicBool::new(false));
    let gave_up = abandoned.clone();
    let (tx, rx) = std::sync::mpsc::channel::<Result<(), String>>();
    tauri::async_runtime::spawn(async move {
        let res = check_and_install(&app2, &meta, &gave_up).await;
        let _ = tx.send(res);
    });
    let res = rx.recv_timeout(START_BUDGET).unwrap_or_else(|_| {
        abandoned.store(true, Ordering::SeqCst);
        Err("no answer in time, update stays pending".into())
    });
    if let Err(e) = res {
        log(app, "warn", &format!("update at start: {e}"));
    }
}

async fn check_and_install(app: &AppHandle, meta: &Cached, abandoned: &AtomicBool) -> Result<(), String> {
    let check = updater(app, START_TIMEOUT)?.check().await;
    match check {
        Ok(Some(u)) if u.version == meta.version => {
            let bytes = std::fs::read(cache_dir(app).join(CACHE_EXE)).map_err(|e| e.to_string())?;
            if abandoned.load(Ordering::SeqCst) {
                return Ok(()); // the app is already starting normally
            }
            // Verifies against the fresh signature, then exits on success.
            u.install(&bytes).map_err(|e| {
                discard(&cache_dir(app));
                format!("cached update rejected: {e}")
            })
        }
        Ok(_) => {
            discard(&cache_dir(app)); // superseded or withdrawn
            Ok(())
        }
        Err(e) => Err(format!("offline, update stays pending: {e}")),
    }
}

#[tauri::command]
pub async fn update_check(
    app: AppHandle,
    pending: State<'_, PendingUpdate>,
) -> Result<Option<UpdateInfo>, String> {
    let update = updater(&app, Duration::from_secs(30))?.check().await.map_err(|e| e.to_string())?;
    let info = update.as_ref().map(|u| UpdateInfo {
        version: u.version.clone(),
        notes: u.body.clone(),
        date: u.date.map(|d| d.to_string()),
    });
    *pending.0.lock().unwrap_or_else(|e| e.into_inner()) = update;
    Ok(info)
}

/// Downloads the announced update in the background (verified by the
/// plugin) and keeps it for the next start. No-op when already cached.
#[tauri::command]
pub async fn update_download(app: AppHandle, pending: State<'_, PendingUpdate>) -> Result<(), String> {
    let update = pending.0.lock().unwrap_or_else(|e| e.into_inner()).clone().ok_or_else(|| "no_update".to_string())?;
    let dir = cache_dir(&app);
    if cached_meta(&dir).is_some_and(|m| m.version == update.version) && dir.join(CACHE_EXE).exists() {
        return Ok(());
    }
    discard(&dir);
    let bytes = update.download(|_, _| {}, || {}).await.map_err(|e| e.to_string())?;
    std::fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    // Installer first, metadata last: the metadata marks a complete download.
    write_atomic(&dir.join(CACHE_EXE), &bytes).map_err(|e| e.to_string())?;
    let meta = serde_json::to_vec(&Cached { version: update.version.clone() }).map_err(|e| e.to_string())?;
    write_atomic(&dir.join(CACHE_META), &meta).map_err(|e| e.to_string())
}

/// Drops a downloaded update (automatic updates switched off).
#[tauri::command]
pub fn update_discard(app: AppHandle) {
    discard(&cache_dir(&app));
}

/// Installs now: the cached download if it matches, otherwise downloads.
/// On Windows the installer takes over and the app exits; it is restarted
/// after the installation.
#[tauri::command]
pub async fn update_install(app: AppHandle, pending: State<'_, PendingUpdate>) -> Result<(), String> {
    let update = pending.0.lock().unwrap_or_else(|e| e.into_inner()).take().ok_or_else(|| "no_update".to_string())?;
    let dir = cache_dir(&app);
    if cached_meta(&dir).is_some_and(|m| m.version == update.version)
        && let Ok(bytes) = std::fs::read(dir.join(CACHE_EXE))
    {
        // install() verifies again; a bad cache falls through to a download.
        if let Err(e) = update.install(&bytes) {
            log(&app, "warn", &format!("cached update rejected: {e}"));
            discard(&dir);
        }
    }
    let mut done: u64 = 0;
    let emitter = app.clone();
    update
        .download_and_install(
            move |chunk, total| {
                done += chunk as u64;
                let _ = emitter.emit("update-progress", Progress { done, total });
            },
            || {},
        )
        .await
        .map_err(|e| e.to_string())?;
    // Not reached on Windows (the updater exits the process).
    app.restart();
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn endpoint_is_https() {
        assert_eq!(endpoint().unwrap().scheme(), "https");
    }

    #[test]
    fn old_cache_metadata_still_parses() {
        // Written by versions that updated from the connected server.
        let meta: Cached = serde_json::from_str(r#"{"version":"1.2.3","server":"https://chat.example.org"}"#).unwrap();
        assert_eq!(meta.version, "1.2.3");
    }
}
