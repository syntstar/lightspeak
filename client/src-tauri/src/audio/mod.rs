//! Native audio helpers (stream audio capture, window → process mapping,
//! our name in the volume mixer).

#[cfg(windows)]
pub mod branding;
#[cfg(windows)]
pub mod loopback;
#[cfg(windows)]
pub mod process;
#[cfg(windows)]
pub mod sessions;
