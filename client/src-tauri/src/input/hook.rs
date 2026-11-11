//! Windows low-level keyboard/mouse hook (WH_KEYBOARD_LL / WH_MOUSE_LL).
//!
//! The hook procedures run on the thread that installed them, inside its
//! message loop, so all per-hook state lives in a thread-local. The procedures
//! only update a small set and push edges into a channel; a second short-lived
//! thread applies the PTT release delay and calls the sink. Both threads exist
//! only while an `InputHook` is alive.

use super::{Action, HookConfig, InputEvent, KeyCode, MOUSE_BASE, Sink, normalize};
use std::cell::RefCell;
use std::collections::HashSet;
use std::sync::mpsc::{self, Receiver, RecvTimeoutError, Sender};
use std::thread::JoinHandle;
use std::time::{Duration, Instant};
use windows::Win32::Foundation::{CloseHandle, HANDLE, HINSTANCE, HWND, LPARAM, LRESULT, WPARAM};
use windows::Win32::Security::{GetTokenInformation, TOKEN_ELEVATION, TOKEN_QUERY, TokenElevation};
use windows::Win32::System::LibraryLoader::GetModuleHandleW;
use windows::Win32::System::Threading::{
    GetCurrentProcess, GetCurrentThreadId, OpenProcess, OpenProcessToken, PROCESS_QUERY_LIMITED_INFORMATION,
};
use windows::Win32::UI::Accessibility::{HWINEVENTHOOK, SetWinEventHook, UnhookWinEvent};
use windows::Win32::UI::Input::KeyboardAndMouse::GetAsyncKeyState;
use windows::Win32::UI::WindowsAndMessaging::{
    CallNextHookEx, EVENT_SYSTEM_FOREGROUND, GetForegroundWindow, GetMessageW, GetWindowThreadProcessId, HC_ACTION,
    HHOOK, KBDLLHOOKSTRUCT, KillTimer, LLKHF_UP, MSG, MSLLHOOKSTRUCT, PostThreadMessageW, SetTimer, SetWindowsHookExW,
    UnhookWindowsHookEx, WH_KEYBOARD_LL, WH_MOUSE_LL, WINEVENT_OUTOFCONTEXT, WM_MBUTTONDOWN, WM_MBUTTONUP, WM_QUIT,
    WM_TIMER, WM_XBUTTONDOWN, WM_XBUTTONUP, XBUTTON1,
};

/// While PTT is held we re-check the physical key state every 250 ms so a
/// missed key-up (lock screen, elevated window, sleep) can never leave the
/// microphone open.
const STUCK_KEY_CHECK_MS: u32 = 250;
const TIMER_ID: usize = 0x4C56;

enum Raw {
    Ptt(bool),
    Toggle(Action),
    Elevated(bool),
    Recorded(Vec<KeyCode>),
}

enum Mode {
    Bindings { cfg: HookConfig, active: Vec<bool> },
    Record { max: Vec<KeyCode> },
}

struct State {
    mode: Mode,
    pressed: HashSet<KeyCode>,
    tx: Sender<Raw>,
    timer_on: bool,
    last_elevated: bool,
}

thread_local! {
    static STATE: RefCell<Option<State>> = const { RefCell::new(None) };
}

pub struct InputHook {
    thread_id: u32,
    hook_thread: Option<JoinHandle<()>>,
    forward_thread: Option<JoinHandle<()>>,
}

impl InputHook {
    /// Installs the hook for the given bindings (PTT / toggles).
    pub fn start(cfg: HookConfig, sink: Sink) -> Result<Self, String> {
        let needs_mouse = cfg.bindings.iter().flat_map(|b| b.keys.iter()).any(|&k| k >= MOUSE_BASE);
        let has_ptt = cfg.bindings.iter().any(|b| b.action == Action::PushToTalk);
        let delay = Duration::from_millis(u64::from(cfg.release_delay_ms.min(1000)));
        let cfg = HookConfig {
            bindings: cfg
                .bindings
                .into_iter()
                .map(|mut b| {
                    b.keys = b.keys.into_iter().map(normalize).collect();
                    b
                })
                .collect(),
            ..cfg
        };
        let n = cfg.bindings.len();
        Self::spawn(Mode::Bindings { cfg, active: vec![false; n] }, needs_mouse, has_ptt, delay, sink)
    }

    /// Installs the hook in recording mode: reports the next key combination.
    pub fn record(sink: Sink) -> Result<Self, String> {
        Self::spawn(Mode::Record { max: Vec::new() }, true, false, Duration::ZERO, sink)
    }

    fn spawn(mode: Mode, mouse: bool, watch_foreground: bool, delay: Duration, sink: Sink) -> Result<Self, String> {
        let (tx, rx) = mpsc::channel::<Raw>();
        let (ready_tx, ready_rx) = mpsc::sync_channel::<Result<u32, String>>(1);

        let hook_thread = std::thread::Builder::new()
            .name("lv-input-hook".into())
            .spawn(move || hook_thread_main(mode, tx, mouse, watch_foreground, ready_tx))
            .map_err(|e| e.to_string())?;

        let thread_id = match ready_rx.recv() {
            Ok(Ok(id)) => id,
            Ok(Err(e)) => {
                let _ = hook_thread.join();
                return Err(e);
            }
            Err(_) => return Err("input hook thread died".into()),
        };

        let forward_thread = std::thread::Builder::new()
            .name("lv-input-forward".into())
            .spawn(move || forward_main(rx, delay, sink))
            .map_err(|e| e.to_string())?;

        Ok(Self { thread_id, hook_thread: Some(hook_thread), forward_thread: Some(forward_thread) })
    }

    pub fn stop(mut self) {
        self.shutdown();
    }

    fn shutdown(&mut self) {
        if let Some(t) = self.hook_thread.take() {
            // SAFETY: posting WM_QUIT to a thread we own; failure only means it already exited.
            let _ = unsafe { PostThreadMessageW(self.thread_id, WM_QUIT, WPARAM(0), LPARAM(0)) };
            let _ = t.join();
        }
        if let Some(t) = self.forward_thread.take() {
            // The channel sender lived in the hook thread's state, so the
            // forwarder ends as soon as that thread is gone.
            let _ = t.join();
        }
    }
}

impl Drop for InputHook {
    fn drop(&mut self) {
        self.shutdown();
    }
}

fn hook_thread_main(
    mode: Mode,
    tx: Sender<Raw>,
    mouse: bool,
    watch_foreground: bool,
    ready: mpsc::SyncSender<Result<u32, String>>,
) {
    STATE.with(|s| {
        *s.borrow_mut() = Some(State { mode, pressed: HashSet::new(), tx, timer_on: false, last_elevated: false })
    });

    // SAFETY: plain Win32 calls; hook procedures are `extern "system"` functions
    // with the documented signatures and live for the whole program.
    unsafe {
        let hmod = GetModuleHandleW(None).ok().map(|m| HINSTANCE(m.0));
        let kb = match SetWindowsHookExW(WH_KEYBOARD_LL, Some(kb_proc), hmod, 0) {
            Ok(h) => h,
            Err(e) => {
                let _ = ready.send(Err(format!("keyboard hook: {e}")));
                STATE.with(|s| s.borrow_mut().take());
                return;
            }
        };
        let ms: Option<HHOOK> =
            if mouse { SetWindowsHookExW(WH_MOUSE_LL, Some(mouse_proc), hmod, 0).ok() } else { None };
        let fg: Option<HWINEVENTHOOK> = if watch_foreground && !process_is_elevated(GetCurrentProcess()) {
            let h = SetWinEventHook(
                EVENT_SYSTEM_FOREGROUND,
                EVENT_SYSTEM_FOREGROUND,
                None,
                Some(foreground_proc),
                0,
                0,
                WINEVENT_OUTOFCONTEXT,
            );
            check_foreground(GetForegroundWindow());
            (!h.is_invalid()).then_some(h)
        } else {
            None
        };

        let _ = ready.send(Ok(GetCurrentThreadId()));

        let mut msg = MSG::default();
        loop {
            let r = GetMessageW(&mut msg, None, 0, 0);
            if r.0 <= 0 {
                break; // WM_QUIT (0) or error (-1)
            }
            if msg.message == WM_TIMER && msg.wParam.0 == TIMER_ID {
                verify_physical_state();
            }
        }

        let _ = KillTimer(None, TIMER_ID);
        if let Some(h) = fg {
            let _ = UnhookWinEvent(h);
        }
        if let Some(h) = ms {
            let _ = UnhookWindowsHookEx(h);
        }
        let _ = UnhookWindowsHookEx(kb);
    }
    // Dropping the state drops the Sender and ends the forwarder.
    STATE.with(|s| s.borrow_mut().take());
}

fn forward_main(rx: Receiver<Raw>, delay: Duration, sink: Sink) {
    let mut ptt_on = false;
    while let Ok(ev) = rx.recv() {
        match ev {
            Raw::Ptt(true) => {
                if !ptt_on {
                    ptt_on = true;
                    sink(InputEvent::Ptt { active: true });
                }
            }
            Raw::Ptt(false) => {
                // Release delay: if PTT is pressed again before it elapses, keep talking.
                let deadline = Instant::now() + delay;
                let mut cancelled = false;
                loop {
                    let left = deadline.saturating_duration_since(Instant::now());
                    if left.is_zero() {
                        break;
                    }
                    match rx.recv_timeout(left) {
                        Ok(Raw::Ptt(true)) => {
                            cancelled = true;
                            break;
                        }
                        Ok(other) => deliver(&sink, other),
                        Err(RecvTimeoutError::Timeout | RecvTimeoutError::Disconnected) => break,
                    }
                }
                if !cancelled && ptt_on {
                    ptt_on = false;
                    sink(InputEvent::Ptt { active: false });
                }
            }
            other => deliver(&sink, other),
        }
    }
    if ptt_on {
        // Hook stopped while talking: never leave the microphone open.
        sink(InputEvent::Ptt { active: false });
    }
}

fn deliver(sink: &Sink, ev: Raw) {
    match ev {
        Raw::Toggle(action) => sink(InputEvent::Toggle { action }),
        Raw::Elevated(elevated) => sink(InputEvent::ElevatedForeground { elevated }),
        Raw::Recorded(keys) => sink(InputEvent::Recorded { keys }),
        Raw::Ptt(active) => sink(InputEvent::Ptt { active }),
    }
}

fn on_key(code: KeyCode, down: bool) {
    STATE.with(|s| {
        let mut guard = s.borrow_mut();
        let Some(st) = guard.as_mut() else { return };
        let changed = if down { st.pressed.insert(code) } else { st.pressed.remove(&code) };
        if !changed {
            return; // auto-repeat or unknown key-up
        }
        match &mut st.mode {
            Mode::Bindings { cfg, active } => {
                let mut ptt_now = None;
                for (i, b) in cfg.bindings.iter().enumerate() {
                    let now = !b.keys.is_empty() && b.keys.iter().all(|k| st.pressed.contains(k));
                    if now == active[i] {
                        continue;
                    }
                    active[i] = now;
                    match b.action {
                        Action::PushToTalk => {
                            let _ = st.tx.send(Raw::Ptt(now));
                            ptt_now = Some(now);
                        }
                        a if now => {
                            let _ = st.tx.send(Raw::Toggle(a));
                        }
                        _ => {}
                    }
                }
                if let Some(on) = ptt_now {
                    set_timer(st, on);
                }
            }
            Mode::Record { max } => {
                if st.pressed.len() > max.len() {
                    *max = st.pressed.iter().copied().collect();
                }
                if st.pressed.is_empty() && !max.is_empty() {
                    let mut keys = std::mem::take(max);
                    // Modifiers first, then the rest, for a stable display order.
                    keys.sort_by_key(|&k| (!(0x10..=0x12).contains(&k) && k != 0x5B && k != 0x5C, k));
                    let _ = st.tx.send(Raw::Recorded(keys));
                }
            }
        }
    });
}

fn set_timer(st: &mut State, on: bool) {
    // SAFETY: thread timers on the hook thread; messages arrive in its loop.
    unsafe {
        if on && !st.timer_on {
            SetTimer(None, TIMER_ID, STUCK_KEY_CHECK_MS, None);
            st.timer_on = true;
        } else if !on && st.timer_on {
            let _ = KillTimer(None, TIMER_ID);
            st.timer_on = false;
        }
    }
}

/// Releases keys that are no longer physically held (missed key-up).
fn verify_physical_state() {
    let stale: Vec<KeyCode> = STATE.with(|s| {
        let guard = s.borrow();
        let Some(st) = guard.as_ref() else { return Vec::new() };
        st.pressed
            .iter()
            .copied()
            .filter(|&k| {
                let vk = match k {
                    k if k == MOUSE_BASE + 3 => 0x04,
                    k if k == MOUSE_BASE + 4 => 0x05,
                    k if k == MOUSE_BASE + 5 => 0x06,
                    k => k as i32,
                };
                // SAFETY: simple key-state query.
                (unsafe { GetAsyncKeyState(vk) } as u16 & 0x8000) == 0
            })
            .collect()
    });
    for k in stale {
        on_key(k, false);
    }
}

unsafe extern "system" fn kb_proc(code: i32, wparam: WPARAM, lparam: LPARAM) -> LRESULT {
    if code == HC_ACTION as i32 {
        // SAFETY: for WH_KEYBOARD_LL with HC_ACTION, lparam points to a KBDLLHOOKSTRUCT.
        let info = unsafe { &*(lparam.0 as *const KBDLLHOOKSTRUCT) };
        on_key(normalize(info.vkCode), !info.flags.contains(LLKHF_UP));
    }
    // Never swallow input: the game must still see the key.
    unsafe { CallNextHookEx(None, code, wparam, lparam) }
}

unsafe extern "system" fn mouse_proc(code: i32, wparam: WPARAM, lparam: LPARAM) -> LRESULT {
    if code == HC_ACTION as i32 {
        let msg = wparam.0 as u32;
        let ev = match msg {
            WM_MBUTTONDOWN => Some((MOUSE_BASE + 3, true)),
            WM_MBUTTONUP => Some((MOUSE_BASE + 3, false)),
            WM_XBUTTONDOWN | WM_XBUTTONUP => {
                // SAFETY: for WH_MOUSE_LL with HC_ACTION, lparam points to a MSLLHOOKSTRUCT.
                let info = unsafe { &*(lparam.0 as *const MSLLHOOKSTRUCT) };
                let button = (info.mouseData >> 16) as u16;
                let key = if button == XBUTTON1 { MOUSE_BASE + 4 } else { MOUSE_BASE + 5 };
                Some((key, msg == WM_XBUTTONDOWN))
            }
            _ => None, // mouse moves and other buttons pass straight through
        };
        if let Some((k, down)) = ev {
            on_key(k, down);
        }
    }
    unsafe { CallNextHookEx(None, code, wparam, lparam) }
}

unsafe extern "system" fn foreground_proc(
    _hook: HWINEVENTHOOK,
    _event: u32,
    hwnd: HWND,
    _idobject: i32,
    _idchild: i32,
    _thread: u32,
    _time: u32,
) {
    check_foreground(hwnd);
}

fn check_foreground(hwnd: HWND) {
    if hwnd.is_invalid() {
        return;
    }
    let mut pid = 0u32;
    // SAFETY: plain query on a window handle.
    unsafe { GetWindowThreadProcessId(hwnd, Some(&mut pid)) };
    if pid == 0 {
        return;
    }
    let elevated = unsafe {
        match OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, pid) {
            Ok(h) => {
                let e = process_is_elevated(h);
                let _ = CloseHandle(h);
                e
            }
            Err(_) => false,
        }
    };
    STATE.with(|s| {
        if let Some(st) = s.borrow_mut().as_mut()
            && st.last_elevated != elevated
        {
            st.last_elevated = elevated;
            let _ = st.tx.send(Raw::Elevated(elevated));
        }
    });
}

/// True if the process runs elevated. If we may not even read its token, it is
/// almost certainly elevated (UIPI), which is exactly the case we warn about.
fn process_is_elevated(process: HANDLE) -> bool {
    // SAFETY: token query with correctly sized out buffer; handle closed after use.
    unsafe {
        let mut token = HANDLE::default();
        if OpenProcessToken(process, TOKEN_QUERY, &mut token).is_err() {
            return true;
        }
        let mut elev = TOKEN_ELEVATION::default();
        let mut len = 0u32;
        let ok = GetTokenInformation(
            token,
            TokenElevation,
            Some(&mut elev as *mut _ as *mut core::ffi::c_void),
            size_of::<TOKEN_ELEVATION>() as u32,
            &mut len,
        )
        .is_ok();
        let _ = CloseHandle(token);
        ok && elev.TokenIsElevated != 0
    }
}
