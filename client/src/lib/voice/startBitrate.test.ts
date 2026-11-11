import { describe, expect, it } from "vitest";
import { withStartBitrate } from "./startBitrate";

const SDP = [
  "v=0",
  "o=- 1 2 IN IP4 127.0.0.1",
  "s=-",
  "t=0 0",
  "a=group:BUNDLE 0 1 2",
  "m=audio 9 UDP/TLS/RTP/SAVPF 111",
  "a=mid:0",
  "a=rtpmap:111 opus/48000/2",
  "a=fmtp:111 minptime=10;useinbandfec=1",
  "m=video 9 UDP/TLS/RTP/SAVPF 96 97 125 107",
  "a=mid:1",
  "a=rtpmap:96 VP8/90000",
  "a=rtpmap:97 rtx/90000",
  "a=fmtp:97 apt=96",
  "a=rtpmap:125 H264/90000",
  "a=fmtp:125 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
  "a=rtpmap:107 rtx/90000",
  "a=fmtp:107 apt=125",
  "m=video 9 UDP/TLS/RTP/SAVPF 125",
  "a=mid:2",
  "a=fmtp:125 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f;x-google-start-bitrate=300",
  "a=rtpmap:125 H264/90000",
  "",
].join("\r\n");

describe("withStartBitrate", () => {
  const out = withStartBitrate(SDP, 5000);
  const lines = out.split("\r\n");

  it("adds the hint to H.264 in every video section, replacing an old value", () => {
    const h264 = lines.filter((l) => l.startsWith("a=fmtp:125 "));
    expect(h264).toHaveLength(2);
    for (const l of h264) {
      expect(l).toContain("packetization-mode=1");
      expect(l.match(/x-google-start-bitrate=/g)).toHaveLength(1);
      expect(l.endsWith(";x-google-start-bitrate=5000")).toBe(true);
    }
  });

  it("gives VP8 its own fmtp line right after the rtpmap", () => {
    const i = lines.indexOf("a=rtpmap:96 VP8/90000");
    expect(lines[i + 1]).toBe("a=fmtp:96 x-google-start-bitrate=5000");
  });

  it("leaves audio, rtx and the session part alone, keeps line endings", () => {
    expect(lines).toContain("a=fmtp:111 minptime=10;useinbandfec=1");
    expect(lines).toContain("a=fmtp:97 apt=96");
    expect(lines).toContain("a=fmtp:107 apt=125");
    expect(out.startsWith("v=0\r\no=- 1 2")).toBe(true);
    expect(out.endsWith("\r\n")).toBe(true);
    expect(out.split("\n").every((l, i, a) => i === a.length - 1 || l.endsWith("\r"))).toBe(true);
  });

  it("is idempotent", () => {
    expect(withStartBitrate(out, 5000)).toBe(out);
  });
});
