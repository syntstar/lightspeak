import { describe, expect, it } from "vitest";
import {
  PreviewGate,
  RAMP_WINDOW_MS,
  RESTORE_BPS,
  RESTORE_MS,
  RampTracker,
  STARVE_MS,
  type LayerSample,
} from "./streamRamp";

describe("PreviewGate", () => {
  const wanted = { fullWanted: true, previewActive: true, fullStarved: false, fullTargetBps: 0 };
  const starved = { ...wanted, fullStarved: true };
  const dropped = () => {
    const g = new PreviewGate();
    g.update(starved, 0);
    g.update(starved, STARVE_MS);
    return g;
  };

  it("drops the preview layer only after lasting starvation", () => {
    const g = new PreviewGate();
    expect(g.update(starved, 0)).toBe(false);
    expect(g.update(starved, STARVE_MS - 1)).toBe(false);
    expect(g.update(starved, STARVE_MS)).toBe(true);
  });

  it("restarts the wait after a good check", () => {
    const g = new PreviewGate();
    g.update(starved, 0);
    g.update(wanted, 1000);
    expect(g.update(starved, STARVE_MS)).toBe(false);
  });

  it("does not act on a preview layer dynacast already paused", () => {
    const g = new PreviewGate();
    const paused = { ...starved, previewActive: false };
    g.update(paused, 0);
    expect(g.update(paused, STARVE_MS)).toBe(false);
  });

  it("stays off although dynacast re-enabled the preview layer", () => {
    expect(dropped().update(starved, STARVE_MS + 1500)).toBe(true);
  });

  it("restores the preview layer once only small tiles are left", () => {
    expect(dropped().update({ ...wanted, fullWanted: false }, STARVE_MS + 1500)).toBe(false);
  });

  it("restores the preview layer after a lasting high target", () => {
    const g = dropped();
    const ok = { ...wanted, previewActive: false, fullTargetBps: RESTORE_BPS };
    expect(g.update(ok, 10_000)).toBe(true);
    expect(g.update({ ...ok, fullTargetBps: 1_000_000 }, 20_000)).toBe(true);
    expect(g.update(ok, 21_000)).toBe(true);
    expect(g.update(ok, 21_000 + RESTORE_MS)).toBe(false);
  });
});

const MAX = 5_000_000;
const TICK = 1500;

/** Runs samples one tick apart and returns the ramp flag after each. */
function run(tr: RampTracker, samples: (LayerSample | null)[], start = 0): boolean[] {
  return samples.map((s, i) => tr.update(s, MAX, start + (i + 1) * TICK));
}

/** Sample with a target and a send rate (bit/s) at tick i. */
function sample(
  i: number,
  targetBps: number,
  sendBps: number,
  extra: Partial<LayerSample> = {},
): LayerSample {
  return {
    active: true,
    targetBps,
    bytesSent: Math.round((sendBps / 8) * (TICK / 1000) * i),
    bandwidthLimited: false,
    ...extra,
  };
}

describe("RampTracker", () => {
  it("reports ramped up after two checks at target bitrate", () => {
    const tr = new RampTracker(0);
    const r = run(tr, [
      sample(0, 1_000_000, 1_000_000),
      sample(1, 4_000_000, 4_000_000),
      sample(2, 4_500_000, 4_500_000),
    ]);
    expect(r).toEqual([true, true, false]);
  });

  it("stays ramping while the encoder fills a low estimate", () => {
    const tr = new RampTracker(0);
    const r = run(
      tr,
      [0, 1, 2, 3].map((i) => sample(i, 2_000_000, 1_900_000)),
    );
    expect(r.every(Boolean)).toBe(true);
  });

  it("counts a still screen (encoder sends far below the estimate) as ramped up", () => {
    // The case from the live test: the estimate never climbs because little is sent.
    const tr = new RampTracker(0);
    const r = run(
      tr,
      [0, 1, 2, 3].map((i) => sample(i, 1_500_000, 300_000)),
    );
    expect(r.at(-1)).toBe(false);
  });

  it("never reports ramping past the window, even on a slow uplink", () => {
    const tr = new RampTracker(0);
    const n = Math.ceil(RAMP_WINDOW_MS / TICK) + 2;
    const r = run(
      tr,
      Array.from({ length: n }, (_, i) => sample(i, 1_000_000, 1_000_000, { bandwidthLimited: true })),
    );
    expect(r[0]).toBe(true);
    expect(r.at(-1)).toBe(false);
  });

  it("starts a new window when the paused full layer resumes", () => {
    const tr = new RampTracker(0);
    const late = RAMP_WINDOW_MS * 2;
    expect(tr.update(null, MAX, late)).toBe(false);
    expect(tr.update({ active: false, targetBps: 0, bandwidthLimited: false }, MAX, late + TICK)).toBe(false);
    expect(tr.update(sample(0, 500_000, 500_000), MAX, late + 2 * TICK)).toBe(true);
  });

  it("starts a new window on restart (settings change)", () => {
    const tr = new RampTracker(0);
    expect(tr.update(sample(0, 1_000_000, 1_000_000), MAX, 1)).toBe(true);
    expect(tr.update(sample(1, 1_000_000, 1_000_000), MAX, RAMP_WINDOW_MS + 1)).toBe(false);
    tr.restart(RAMP_WINDOW_MS + 2);
    expect(tr.update(sample(2, 1_000_000, 1_000_000), MAX, RAMP_WINDOW_MS + 3)).toBe(true);
  });
});
