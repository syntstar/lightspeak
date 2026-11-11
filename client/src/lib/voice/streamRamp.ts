// Whether our stream's full layer still ramps up, so viewers see
// "Quality improving …". Pure, no SDK.

/** Full layer counts as ramped up from this share of its max. bitrate. */
export const RAMPED = 0.75;
/**
 * The encoder sends less than this share of what the bandwidth estimate
 * allows: the content needs no more (still screen, text), so a lower
 * estimate costs no quality. libwebrtc only raises its estimate while it
 * actually sends, so with such content the target never reaches RAMPED.
 */
export const APP_LIMITED = 0.6;
/**
 * Ramping is reported at most this long after a start, a settings change or
 * a resumed full layer. A lasting limit (slow uplink) is no "improving".
 */
export const RAMP_WINDOW_MS = 20_000;

/** Stats of the full layer's outbound-rtp entry, or null if there is none. */
export interface LayerSample {
  active: boolean;
  targetBps: number;
  bytesSent?: number;
  bandwidthLimited: boolean;
}

export class RampTracker {
  private from: number;
  private okTicks = 0;
  private wasActive = false;
  private last: { bytes: number; at: number } | undefined;
  private ramp = true;

  constructor(now: number) {
    this.from = now;
  }

  /** New ramp after a start or settings change. */
  restart(now: number): void {
    this.from = now;
    this.okTicks = 0;
    this.last = undefined;
    this.ramp = true;
  }

  /**
   * One check. Ramping is reported at once, "ramped up" only after two good
   * checks in a row, so the viewer's hint does not flicker.
   */
  update(s: LayerSample | null, maxBps: number, now: number): boolean {
    const active = !!s?.active;
    // Dynacast resumed the paused full layer: it starts low again.
    if (active && !this.wasActive) this.restart(now);
    this.wasActive = active;

    let sentBps: number | undefined;
    if (active && s?.bytesSent !== undefined) {
      const l = this.last;
      if (l && now > l.at && s.bytesSent >= l.bytes)
        sentBps = ((s.bytesSent - l.bytes) * 8000) / (now - l.at);
      this.last = { bytes: s.bytesSent, at: now };
    } else {
      this.last = undefined;
    }

    const target = s?.targetBps ?? 0;
    const enough = target >= RAMPED * maxBps;
    const unneeded = sentBps !== undefined && target > 0 && sentBps < APP_LIMITED * target;
    const good = active && !s?.bandwidthLimited && (enough || unneeded);
    this.okTicks = good ? this.okTicks + 1 : 0;
    this.ramp = this.ramp ? this.okTicks < 2 : !good;
    if (now - this.from >= RAMP_WINDOW_MS) this.ramp = false;
    return this.ramp;
  }
}

/** Full layer gets no bitrate for this long: the uplink can't carry both layers. */
export const STARVE_MS = 8_000;
/** The preview layer returns once the full layer's target stays this high (both fit). */
export const RESTORE_BPS = 2_000_000;
export const RESTORE_MS = 30_000;

export interface LayerState {
  /** Dynacast wants the full layer, i.e. a viewer is watching it large. */
  fullWanted: boolean;
  previewActive: boolean;
  /** The full layer is enabled, but libwebrtc allocates it nothing. */
  fullStarved: boolean;
  fullTargetBps: number;
}

/**
 * Decides whether the preview layer gives way. libwebrtc always
 * serves the lowest active layer first and starts the next one only above
 * its minimum, so on a slow uplink viewers stay at 360p for good, although
 * the full layer alone would fit and look sharper.
 */
export class PreviewGate {
  private off = false;
  private since: number | undefined;

  /** One check; true = keep the preview layer off. */
  update(s: LayerState, now: number): boolean {
    if (!s.fullWanted) {
      // Only small tiles left: they need the preview layer.
      this.off = false;
      this.since = undefined;
    } else if (this.off ? s.fullTargetBps >= RESTORE_BPS : s.fullStarved && s.previewActive) {
      this.since ??= now;
      if (now - this.since >= (this.off ? RESTORE_MS : STARVE_MS)) {
        this.off = !this.off;
        this.since = undefined;
      }
    } else {
      this.since = undefined;
    }
    return this.off;
  }
}
