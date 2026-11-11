//! Diagnostics (Settings → Performance / About): a small local log file,
//! live resource usage of the app's process tree and the diagnostics report.
//! Nothing here runs unless the UI asks for it; the log is append-only, capped
//! and scrubbed of anything that looks like a token or key.

use std::io::{Read, Write};
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::time::{Instant, SystemTime, UNIX_EPOCH};

const LOG_NAME: &str = "lightspeak.log";
const LOG_OLD: &str = "lightspeak.old.log";
/// The current log is rotated once it reaches this size (one old file kept).
const MAX_LOG: u64 = 512 * 1024;
const MAX_LINE: usize = 2000;
/// Log bytes appended to a diagnostics report.
const REPORT_LOG_TAIL: u64 = 256 * 1024;

pub struct Diag {
    dir: PathBuf,
    write: Mutex<()>,
    /// Last CPU sample: wall clock and summed process times (100 ns units).
    cpu: Mutex<Option<(Instant, u64)>>,
}

impl Diag {
    pub fn new(data_dir: &Path) -> Self {
        Diag { dir: data_dir.join("logs"), write: Mutex::new(()), cpu: Mutex::new(None) }
    }

    pub fn dir(&self) -> &Path {
        &self.dir
    }

    /// Appends one scrubbed line (`<UTC time> [level] message`).
    pub fn log(&self, level: &str, msg: &str) {
        let level = match level {
            "error" | "warn" | "info" => level,
            _ => "info",
        };
        let mut msg: String = scrub(msg).chars().filter(|c| *c != '\r').take(MAX_LINE).collect();
        msg = msg.replace('\n', " ⏎ ");
        let line = format!("{} [{level}] {msg}\n", utc_now());
        let Ok(_guard) = self.write.lock() else { return };
        if std::fs::create_dir_all(&self.dir).is_err() {
            return;
        }
        let path = self.dir.join(LOG_NAME);
        if std::fs::metadata(&path).map(|m| m.len() >= MAX_LOG).unwrap_or(false) {
            let _ = std::fs::rename(&path, self.dir.join(LOG_OLD));
        }
        if let Ok(mut f) = std::fs::OpenOptions::new().create(true).append(true).open(&path) {
            let _ = f.write_all(line.as_bytes());
        }
    }

    /// The last `max` bytes of the log (old + current file), lossy UTF-8.
    pub fn tail(&self, max: u64) -> String {
        let _guard = self.write.lock();
        let mut out = Vec::new();
        for name in [LOG_OLD, LOG_NAME] {
            if let Ok(mut f) = std::fs::File::open(self.dir.join(name)) {
                let _ = f.read_to_end(&mut out);
            }
        }
        let start = out.len().saturating_sub(max as usize);
        // Start at a line boundary so the first line is not cut in half.
        let start = out[start..].iter().position(|b| *b == b'\n').map(|p| start + p + 1).filter(|_| start > 0);
        String::from_utf8_lossy(&out[start.unwrap_or(0)..]).into_owned()
    }

    /// Resource usage of our process tree. CPU is the share of all cores
    /// since the previous call (None on the first call).
    pub fn stats(&self) -> ProcStats {
        let (mut s, times) = sample();
        let now = Instant::now();
        if let Ok(mut last) = self.cpu.lock() {
            if let Some((t0, c0)) = *last {
                let wall = now.duration_since(t0).as_nanos() as f64 / 100.0;
                let cores = std::thread::available_parallelism().map(|n| n.get()).unwrap_or(1) as f64;
                if wall > 0.0 {
                    let busy = times.saturating_sub(c0) as f64;
                    s.cpu_percent = Some((busy / (wall * cores) * 100.0).clamp(0.0, 100.0));
                }
            }
            *last = Some((now, times));
        }
        s
    }

    /// Writes the report (+ log tail) to `dir` and returns the file path.
    pub fn export(&self, dir: &Path, report: &str) -> Result<PathBuf, String> {
        std::fs::create_dir_all(dir).map_err(|e| e.to_string())?;
        let stamp = utc_now().replace([':', '-'], "").replace('T', "-").trim_end_matches('Z').to_string();
        let path = dir.join(format!("lightspeak-diagnose-{stamp}.txt"));
        let mut f = std::fs::File::create(&path).map_err(|e| e.to_string())?;
        let body = format!("{}\n\n----- log -----\n{}", scrub(report), self.tail(REPORT_LOG_TAIL));
        f.write_all(body.as_bytes()).map_err(|e| e.to_string())?;
        Ok(path)
    }
}

#[derive(serde::Serialize, Default, Debug)]
#[serde(rename_all = "camelCase")]
pub struct ProcStats {
    /// Sum of working sets (Task Manager "Memory", incl. shared pages), bytes.
    pub working_set: u64,
    /// Sum of private bytes, bytes.
    pub private_bytes: u64,
    pub cpu_percent: Option<f64>,
    pub processes: u32,
}

#[cfg(windows)]
fn sample() -> (ProcStats, u64) {
    use windows::Win32::Foundation::{CloseHandle, FILETIME};
    use windows::Win32::System::ProcessStatus::{K32GetProcessMemoryInfo, PROCESS_MEMORY_COUNTERS_EX};
    use windows::Win32::System::Threading::{GetProcessTimes, OpenProcess, PROCESS_QUERY_LIMITED_INFORMATION};

    let mut s = ProcStats::default();
    let mut times = 0u64;
    let ft = |f: FILETIME| ((f.dwHighDateTime as u64) << 32) | f.dwLowDateTime as u64;
    for pid in crate::webview_mem::own_process_tree() {
        // SAFETY: plain Win32 calls with an owned handle closed below; the
        // out structs are sized as the API expects.
        unsafe {
            let Ok(h) = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, pid) else { continue };
            let mut pmc = PROCESS_MEMORY_COUNTERS_EX {
                cb: std::mem::size_of::<PROCESS_MEMORY_COUNTERS_EX>() as u32,
                ..Default::default()
            };
            if K32GetProcessMemoryInfo(h, &mut pmc as *mut _ as *mut _, pmc.cb).as_bool() {
                s.working_set += pmc.WorkingSetSize as u64;
                s.private_bytes += pmc.PrivateUsage as u64;
                s.processes += 1;
            }
            let (mut c, mut e, mut k, mut u) = Default::default();
            if GetProcessTimes(h, &mut c, &mut e, &mut k, &mut u).is_ok() {
                times += ft(k) + ft(u);
            }
            let _ = CloseHandle(h);
        }
    }
    (s, times)
}

#[cfg(not(windows))]
fn sample() -> (ProcStats, u64) {
    (ProcStats::default(), 0)
}

/// Removes secrets from free text: bearer tokens, `token=`/`sig=`-style
/// query values and long opaque strings (tokens, keys, JWTs).
pub fn scrub(s: &str) -> String {
    const KEYS: [&str; 6] = ["bearer ", "token=", "sig=", "password=", "secret=", "key="];
    const LONG: usize = 32;
    let opaque = |c: char| c.is_ascii_alphanumeric() || matches!(c, '-' | '_' | '+' | '/' | '=' | '.' | '%');
    let chars: Vec<char> = s.chars().collect();
    let lower: Vec<char> = s.to_lowercase().chars().collect();
    let same_len = lower.len() == chars.len();
    let keys: Vec<Vec<char>> = KEYS.iter().map(|k| k.chars().collect()).collect();
    let mut out = String::with_capacity(s.len());
    let mut i = 0;
    while i < chars.len() {
        // Keyword followed by a value: keep the keyword, mask the value.
        if same_len && let Some(k) = keys.iter().find(|k| lower[i..].starts_with(k)) {
            let klen = k.len();
            out.extend(&chars[i..i + klen]);
            i += klen;
            let start = i;
            while i < chars.len() && opaque(chars[i]) && chars[i] != '&' {
                i += 1;
            }
            if i > start {
                out.push_str("***");
            }
            continue;
        }
        if opaque(chars[i]) {
            let start = i;
            while i < chars.len() && opaque(chars[i]) {
                i += 1;
            }
            let run = &chars[start..i];
            // Paths and URLs consist of short segments; only long unbroken
            // runs (no '/' or '.' separators splitting them) are opaque.
            let longest = run.split(|c| *c == '/' || *c == '.').map(|p| p.len()).max().unwrap_or(0);
            if longest >= LONG {
                out.push_str("***");
            } else {
                out.extend(run);
            }
            continue;
        }
        out.push(chars[i]);
        i += 1;
    }
    out
}

/// Current UTC time as `YYYY-MM-DDTHH:MM:SSZ` (no date crate needed).
pub fn utc_now() -> String {
    let secs = SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_secs()).unwrap_or(0) as i64;
    let (days, rem) = (secs.div_euclid(86_400), secs.rem_euclid(86_400));
    // Howard Hinnant's civil_from_days.
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z - era * 146_097;
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = yoe + era * 400 + i64::from(m <= 2);
    format!("{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}Z", rem / 3600, rem % 3600 / 60, rem % 60)
}

/// Opens Explorer with `path` selected (or the folder itself).
pub fn reveal(path: &Path, select: bool) {
    let mut cmd = std::process::Command::new("explorer.exe");
    if select {
        cmd.arg(format!("/select,{}", path.display()));
    } else {
        cmd.arg(path);
    }
    let _ = cmd.spawn();
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn scrub_masks_secrets() {
        assert_eq!(scrub("Authorization: Bearer abc.def-123"), "Authorization: Bearer ***");
        assert_eq!(scrub("GET /files/1?sig=deadbeef&exp=5"), "GET /files/1?sig=***&exp=5");
        assert_eq!(scrub("ws://x/ws?token=abc"), "ws://x/ws?token=***");
        let jwt = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4ifQ.abc";
        assert_eq!(scrub(&format!("jwt {jwt} end")), "jwt *** end");
        let key = "q8L5n0VhH0Sx1vS3n3c4m0b5KxR6WbJxQyP2u1nYw1o=";
        assert_eq!(scrub(key), "***");
    }

    #[test]
    fn scrub_keeps_normal_text() {
        let s = "voice: joined 110552959984599042 via https://chat.example.org/api/v1/voice/join (ICE ok)";
        assert_eq!(scrub(s), s);
        assert_eq!(scrub("Grüße – ok"), "Grüße – ok");
    }

    #[test]
    fn utc_format() {
        let s = utc_now();
        assert_eq!(s.len(), 20);
        assert!(s.ends_with('Z') && s.as_bytes()[10] == b'T');
    }

    #[test]
    fn log_appends_and_tails() {
        let dir = std::env::temp_dir().join(format!("ls-diag-test-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let d = Diag::new(&dir);
        let big = "x".repeat(1000);
        for i in 0..600 {
            d.log("warn", &format!("{i} {}", &big[..30]));
        }
        d.log("error", "last line token=secret");
        let tail = d.tail(4096);
        assert!(tail.ends_with("[error] last line token=***\n"), "{tail}");
        assert!(!tail.starts_with('\n'));
        let _ = std::fs::remove_dir_all(&dir);
    }
}
