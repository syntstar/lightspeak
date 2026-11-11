//! Small Windows integrations implemented directly on Win32 instead of extra
//! crates: credential store, registry (autostart, URL scheme), idle time.

pub mod credentials;
pub mod registry;
pub mod taskbar;
pub mod toast;

use std::path::PathBuf;

/// Runtime profile. `--profile <name>` lets two clients run side by side on
/// one PC for testing; each profile has its own data folder
/// and credential entries.
#[derive(Clone, Debug)]
pub struct Profile {
    pub name: String,
    pub data_dir: PathBuf,
    pub start_minimized: bool,
}

impl Profile {
    pub fn is_default(&self) -> bool {
        self.name == "default"
    }

    /// Credential Manager name for a per-server secret
    /// (`Lightspeak/<profile>/<host[:port]>/<key>`). Sessions and device keys
    /// are kept apart per server, so a server never sees another one's token
    /// and accounts on different servers cannot be linked by their key.
    pub fn credential_target(&self, server: &str, key: &str) -> Result<String, String> {
        let host = server_key(server).ok_or("invalid_server")?;
        Ok(format!("Lightspeak/{}/{}/{}", self.name, host, key))
    }
}

/// Normalizes a server origin to `host[:port]` (lowercase). Accepts
/// `https://host[:port]` and, for loopback hosts only, `http://`.
pub fn server_key(origin: &str) -> Option<String> {
    let o = origin.strip_suffix('/').unwrap_or(origin);
    let (https, rest) = match (o.strip_prefix("https://"), o.strip_prefix("http://")) {
        (Some(r), _) => (true, r),
        (None, Some(r)) => (false, r),
        _ => return None,
    };
    let host = valid_host(rest)?;
    (https || is_loopback(&host)).then_some(host)
}

/// `name[:port]` with a DNS name or IPv4 address; returned in lowercase.
fn valid_host(raw: &str) -> Option<String> {
    let h = raw.to_ascii_lowercase();
    let (name, port) = match h.rsplit_once(':') {
        Some((n, p)) => (n, Some(p)),
        None => (h.as_str(), None),
    };
    let name_ok = !name.is_empty()
        && name.len() <= 253
        && name.chars().all(|c| c.is_ascii_alphanumeric() || c == '.' || c == '-')
        && !name.starts_with(['.', '-'])
        && !name.ends_with(['.', '-'])
        && !name.contains("..");
    let port_ok = port.is_none_or(|p| {
        !p.is_empty()
            && p.len() <= 5
            && p.chars().all(|c| c.is_ascii_digit())
            && matches!(p.parse::<u32>(), Ok(1..=65535))
    });
    (name_ok && port_ok).then_some(h)
}

fn is_loopback(host: &str) -> bool {
    let name = host.rsplit_once(':').map_or(host, |(n, _)| n);
    name == "localhost" || name == "127.0.0.1"
}

fn valid_profile(name: &str) -> bool {
    !name.is_empty()
        && name.len() <= 32
        && name.chars().all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-' || c == '_')
}

pub fn detect_profile() -> Profile {
    let args: Vec<String> = std::env::args().collect();
    let mut name = "default".to_string();
    let mut start_minimized = false;
    let mut i = 1;
    while i < args.len() {
        match args[i].as_str() {
            "--profile" if i + 1 < args.len() => {
                if valid_profile(&args[i + 1]) {
                    name = args[i + 1].clone();
                }
                i += 1;
            }
            "--minimized" => start_minimized = true,
            _ => {}
        }
        i += 1;
    }
    let base = std::env::var_os("APPDATA").map(PathBuf::from).unwrap_or_else(std::env::temp_dir);
    let data_dir = base.join("Lightspeak").join(&name);
    let _ = std::fs::create_dir_all(&data_dir);
    Profile { name, data_dir, start_minimized }
}

/// Seconds since the last keyboard/mouse input (for automatic "away").
pub fn idle_seconds() -> u64 {
    use windows::Win32::System::SystemInformation::GetTickCount;
    use windows::Win32::UI::Input::KeyboardAndMouse::{GetLastInputInfo, LASTINPUTINFO};
    let mut info = LASTINPUTINFO { cbSize: std::mem::size_of::<LASTINPUTINFO>() as u32, dwTime: 0 };
    // SAFETY: plain Win32 calls with a correctly sized struct.
    unsafe {
        if !GetLastInputInfo(&mut info).as_bool() {
            return 0;
        }
        (GetTickCount().wrapping_sub(info.dwTime) / 1000) as u64
    }
}

/// Finds a `lightspeak://invite/<host>/<code>` link among command-line
/// arguments and returns it as the equivalent web invite link
/// (`https://<host>/invite/<code>`). Everything else is rejected.
pub fn invite_from_args<S: AsRef<str>>(args: &[S]) -> Option<String> {
    args.iter().find_map(|a| parse_invite_url(a.as_ref()))
}

pub fn parse_invite_url(url: &str) -> Option<String> {
    let rest = url.strip_prefix("lightspeak://invite/")?;
    let rest = rest.strip_suffix('/').unwrap_or(rest);
    let (host, code) = rest.split_once('/')?;
    let host = valid_host(host)?;
    let ok = (8..=32).contains(&code.len()) && code.chars().all(|c| c.is_ascii_alphanumeric());
    let scheme = if is_loopback(&host) { "http" } else { "https" };
    ok.then(|| format!("{scheme}://{host}/invite/{code}"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn invite_links() {
        assert_eq!(
            parse_invite_url("lightspeak://invite/Chat.Example.org/AbCd2345xy"),
            Some("https://chat.example.org/invite/AbCd2345xy".into())
        );
        assert_eq!(
            parse_invite_url("lightspeak://invite/127.0.0.1:8080/AbCd2345xy/"),
            Some("http://127.0.0.1:8080/invite/AbCd2345xy".into())
        );
        for bad in [
            "lightspeak://invite/AbCd2345xy",
            "lightspeak://invite/host.org/short",
            "lightspeak://invite/host.org/abc def ghij",
            "lightspeak://invite/../../etc",
            "lightspeak://invite/host.org/AbCd2345xy/extra",
            "lightspeak://invite/user@host.org/AbCd2345xy",
            "lightspeak://invite/host.org:99999/AbCd2345xy",
            "lightspeak://settings/x",
            "https://evil/invite/AbCd2345xy",
            "lightspeak://invite/host.org/AbCd2345xy?x=javascript:alert(1)",
        ] {
            assert_eq!(parse_invite_url(bad), None, "{bad}");
        }
    }

    #[test]
    fn server_keys() {
        assert_eq!(server_key("https://Chat.Example.org/"), Some("chat.example.org".into()));
        assert_eq!(server_key("https://1-2-3-4.sslip.io:8443"), Some("1-2-3-4.sslip.io:8443".into()));
        assert_eq!(server_key("http://127.0.0.1:8080"), Some("127.0.0.1:8080".into()));
        assert_eq!(server_key("http://localhost:8080"), Some("localhost:8080".into()));
        for bad in [
            "chat.example.org",
            "http://chat.example.org",
            "http://192.168.1.10:8080",
            "https://chat.example.org/path",
            "https://user@chat.example.org",
            "https://chat.example.org:0",
            "https://-bad.org",
            "https://a..b",
            "ftp://x.org",
        ] {
            assert_eq!(server_key(bad), None, "{bad}");
        }
    }

    #[test]
    fn profiles() {
        assert!(valid_profile("test2"));
        assert!(!valid_profile("../x"));
        assert!(!valid_profile(""));
    }
}
