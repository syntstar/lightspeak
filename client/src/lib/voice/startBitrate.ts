// Start bitrate hint for screen shares.
//
// libwebrtc reads `x-google-start-bitrate` for the send side from the REMOTE
// description. livekit-client writes it into its own offer, which the SFU's
// answer does not echo, so the hint is lost: the estimate stays where the
// audio-only call left it (~300 kbit/s) and the full simulcast layer stays
// paused for ~10 s – viewers see the 360p preview ("very pixelated").
// We add the hint to the answer instead. libwebrtc applies a start value only
// when it differs from the previous one, then backs off within a second to
// what the link really carries – a short, bounded burst.

const VIDEO_CODECS = /^(H264|VP8|VP9|AV1)$/i;
const PARAM = "x-google-start-bitrate";

/**
 * Sets `x-google-start-bitrate=<kbps>` on every video codec of every video
 * section. All sections get the same value, as payload types are shared
 * across the bundle (differing fmtp is a "bundled payload type collision").
 */
export function withStartBitrate(sdp: string, kbps: number): string {
  const nl = sdp.includes("\r\n") ? "\r\n" : "\n";
  const sections: string[][] = [[]];
  for (const line of sdp.split(nl)) {
    if (line.startsWith("m=")) sections.push([]);
    sections[sections.length - 1]!.push(line);
  }
  return sections
    .map((s) => (s[0]?.startsWith("m=video") ? hintSection(s, kbps) : s))
    .flat()
    .join(nl);
}

function hintSection(lines: string[], kbps: number): string[] {
  const pts = new Set<string>();
  for (const l of lines) {
    const m = /^a=rtpmap:(\d+) ([^/]+)\//.exec(l);
    if (m && VIDEO_CODECS.test(m[2]!)) pts.add(m[1]!);
  }
  const done = new Set<string>();
  const out = lines.map((l) => {
    const m = /^a=fmtp:(\d+) (.*)$/.exec(l);
    if (!m || !pts.has(m[1]!)) return l;
    done.add(m[1]!);
    const params = m[2]!.split(";").filter((p) => p && !p.startsWith(`${PARAM}=`));
    return `a=fmtp:${m[1]} ${[...params, `${PARAM}=${kbps}`].join(";")}`;
  });
  // Codecs without parameters (VP8) get their own fmtp line after the rtpmap.
  for (const pt of pts) {
    if (done.has(pt)) continue;
    const at = out.findIndex((l) => l.startsWith(`a=rtpmap:${pt} `));
    out.splice(at + 1, 0, `a=fmtp:${pt} ${PARAM}=${kbps}`);
  }
  return out;
}
