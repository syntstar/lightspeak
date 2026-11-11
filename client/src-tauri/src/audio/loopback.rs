//! Stream audio via WASAPI process loopback (Windows 10 2004+/build 19041+).
//!
//! * Window stream → INCLUDE the process tree that owns the window (only that app).
//! * Screen stream → EXCLUDE the WebView2 browser process tree (system audio
//!   without the Lightspeak voices, so viewers never hear themselves). Our own
//!   pid does not work here: loopback does not see WebView2 as our child.
//!
//! PCM is captured as 48 kHz stereo f32 and pushed to the webview in 10 ms
//! frames (raw bytes over a Tauri channel) where an AudioWorklet turns it into
//! a WebRTC track. The capture thread exists only while streaming.

use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::thread::JoinHandle;

pub const SAMPLE_RATE: usize = 48_000;
pub const CHANNELS: usize = 2;
/// 10 ms of interleaved stereo f32.
pub const FRAME_SAMPLES: usize = SAMPLE_RATE / 100 * CHANNELS;
const FRAME_BYTES: usize = FRAME_SAMPLES * 4;

#[derive(Debug, Clone, Copy)]
pub enum Target {
    /// Only this process and its children (window stream).
    Include(u32),
    /// Everything except this process and its children (screen stream, WebView2 browser).
    Exclude(u32),
}

pub struct LoopbackCapture {
    stop: Arc<AtomicBool>,
    thread: Option<JoinHandle<()>>,
}

impl LoopbackCapture {
    /// Starts capturing. `on_frame` receives 10 ms frames: an 8-byte f64 LE
    /// timestamp followed by 3 840 bytes of f32 LE interleaved stereo. `on_end` is called once if capture stops by itself.
    pub fn start(
        target: Target,
        on_frame: impl Fn(Vec<u8>) + Send + 'static,
        on_end: impl FnOnce(Option<String>) + Send + 'static,
    ) -> Result<Self, String> {
        let stop = Arc::new(AtomicBool::new(false));
        let stop2 = stop.clone();
        let (ready_tx, ready_rx) = std::sync::mpsc::sync_channel::<Result<(), String>>(1);
        let thread = std::thread::Builder::new()
            .name("lv-loopback".into())
            .spawn(move || {
                let res = capture_loop(target, &stop2, &on_frame, &ready_tx);
                if !stop2.load(Ordering::Relaxed) {
                    on_end(res.err());
                }
            })
            .map_err(|e| e.to_string())?;
        match ready_rx.recv() {
            Ok(Ok(())) => Ok(Self { stop, thread: Some(thread) }),
            Ok(Err(e)) => {
                let _ = thread.join();
                Err(e)
            }
            Err(_) => Err("loopback thread died".into()),
        }
    }

    pub fn stop(mut self) {
        self.shutdown();
    }

    fn shutdown(&mut self) {
        self.stop.store(true, Ordering::Relaxed);
        if let Some(t) = self.thread.take() {
            let _ = t.join();
        }
    }
}

impl Drop for LoopbackCapture {
    fn drop(&mut self) {
        self.shutdown();
    }
}

fn capture_loop(
    target: Target,
    stop: &AtomicBool,
    on_frame: &dyn Fn(Vec<u8>),
    ready: &std::sync::mpsc::SyncSender<Result<(), String>>,
) -> Result<(), String> {
    use wasapi::{AudioClient, Direction, SampleType, StreamMode, WaveFormat, initialize_mta};

    let init = || -> Result<_, String> {
        initialize_mta().ok().map_err(|e| format!("com: {e}"))?;
        let (pid, include) = match target {
            Target::Include(p) => (p, true),
            Target::Exclude(p) => (p, false),
        };
        let mut client = AudioClient::new_application_loopback_client(pid, include).map_err(|e| e.to_string())?;
        let fmt = WaveFormat::new(32, 32, &SampleType::Float, SAMPLE_RATE, CHANNELS, None);
        // Process loopback ignores the buffer duration; autoconvert gives us 48k f32.
        let mode = StreamMode::EventsShared { autoconvert: true, buffer_duration_hns: 0 };
        client.initialize_client(&fmt, &Direction::Capture, &mode).map_err(|e| e.to_string())?;
        let event = client.set_get_eventhandle().map_err(|e| e.to_string())?;
        let capture = client.get_audiocaptureclient().map_err(|e| e.to_string())?;
        client.start_stream().map_err(|e| e.to_string())?;
        Ok((client, event, capture))
    };

    let (client, event, capture) = match init() {
        Ok(v) => {
            let _ = ready.send(Ok(()));
            v
        }
        Err(e) => {
            let _ = ready.send(Err(e.clone()));
            return Err(e);
        }
    };

    let mut pending: std::collections::VecDeque<u8> = std::collections::VecDeque::with_capacity(FRAME_BYTES * 8);
    let result = loop {
        if stop.load(Ordering::Relaxed) {
            break Ok(());
        }
        // Process loopback only signals while the target renders audio; a
        // timeout simply means silence, not an error.
        if event.wait_for_event(200).is_err() {
            continue;
        }
        loop {
            match capture.get_next_packet_size() {
                Ok(Some(n)) if n > 0 => {
                    if capture.read_from_device_to_deque(&mut pending).is_err() {
                        break;
                    }
                }
                Ok(_) => break,
                Err(e) => return finish(&client, Err(e.to_string())),
            }
        }
        while pending.len() >= FRAME_BYTES {
            // 8-byte header: capture wall-clock time (ms since epoch, f64 LE)
            // so the UI can show the transport latency in diagnostics.
            let now = std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .map(|d| d.as_secs_f64() * 1000.0)
                .unwrap_or(0.0);
            let mut frame = Vec::with_capacity(8 + FRAME_BYTES);
            frame.extend_from_slice(&now.to_le_bytes());
            frame.extend(pending.drain(..FRAME_BYTES));
            on_frame(frame);
        }
    };
    finish(&client, result)
}

fn finish(client: &wasapi::AudioClient, r: Result<(), String>) -> Result<(), String> {
    let _ = client.stop_stream();
    r
}
