import { describe, expect, it } from "vitest";
import { iceRttMs } from "./iceRtt";

const report = (stats: Record<string, unknown>[]) => ({
  forEach: (cb: (s: Record<string, unknown>) => void) => stats.forEach(cb),
});

describe("iceRttMs", () => {
  it("reads the selected candidate pair", () => {
    expect(
      iceRttMs(
        report([
          { id: "T", type: "transport", selectedCandidatePairId: "P2" },
          {
            id: "P1",
            type: "candidate-pair",
            currentRoundTripTime: 0.3,
            nominated: true,
            state: "succeeded",
          },
          { id: "P2", type: "candidate-pair", currentRoundTripTime: 0.0934 },
        ]),
      ),
    ).toBe(93);
  });

  it("falls back to the nominated pair without transport stats", () => {
    expect(
      iceRttMs(
        report([
          {
            id: "P1",
            type: "candidate-pair",
            currentRoundTripTime: 0.2,
            nominated: false,
            state: "succeeded",
          },
          {
            id: "P2",
            type: "candidate-pair",
            currentRoundTripTime: 0.041,
            nominated: true,
            state: "succeeded",
          },
        ]),
      ),
    ).toBe(41);
  });

  it("is null before a pair has an RTT", () => {
    expect(
      iceRttMs(
        report([
          { id: "T", type: "transport", selectedCandidatePairId: "P" },
          { id: "P", type: "candidate-pair" },
        ]),
      ),
    ).toBeNull();
    expect(iceRttMs(report([]))).toBeNull();
  });
});
