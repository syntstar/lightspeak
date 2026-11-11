// Ping shown in the voice panel: round-trip time of the media path (ICE
// candidate pair, measured by WebRTC's own STUN checks) – the path voice
// actually takes, like Discord's ping.
//
// livekit-client's `engine.client.rtt` is not usable for this: it times the
// second of two pings sent back to back over the signalling WebSocket (TCP +
// TLS + reverse proxy). Through TCP proxies (e.g. VPNs) that answer arrives one
// full round trip after the first, so it showed ~2× the real value
// (measured: 184 ms shown, 91 ms first pong, 93 ms ICE).

type Stat = Record<string, unknown>;

/** Current RTT of the selected ICE candidate pair in ms, or null if unknown. */
export function iceRttMs(report: { forEach(cb: (s: Stat) => void): void }): number | null {
  const all = new Map<string, Stat>();
  let selected: string | undefined;
  report.forEach((s) => {
    if (typeof s.id === "string") all.set(s.id, s);
    if (s.type === "transport" && typeof s.selectedCandidatePairId === "string")
      selected = s.selectedCandidatePairId;
  });
  let pair = selected ? all.get(selected) : undefined;
  // Firefox has no transport stats: the nominated, succeeded pair.
  if (!pair)
    for (const s of all.values())
      if (s.type === "candidate-pair" && s.nominated === true && s.state === "succeeded") {
        pair = s;
        break;
      }
  const rtt = pair?.currentRoundTripTime;
  return typeof rtt === "number" && rtt >= 0 ? Math.round(rtt * 1000) : null;
}
