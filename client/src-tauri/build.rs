fn main() {
    // Updater public key: compiled in when updater.pub exists.
    // The file holds the public half only; the private key never enters the repo.
    println!("cargo:rerun-if-changed=updater.pub");
    println!("cargo:rerun-if-env-changed=LIGHTSPEAK_UPDATE_URL");
    if let Ok(key) = std::fs::read_to_string("updater.pub") {
        let key = key.trim();
        if !key.is_empty() {
            println!("cargo:rustc-env=LIGHTSPEAK_UPDATER_PUBKEY={key}");
        }
    }
    tauri_build::build()
}
