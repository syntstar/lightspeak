// Everything from livekit-client the voice engine needs, in one chunk that is
// only loaded on the first join (lazy import: the SDK is
// ~600 KB and costs nothing while nobody is in a call).
import { DefaultReconnectPolicy } from "livekit-client";
import E2EEWorker from "livekit-client/e2ee-worker?worker";

export { AudioPresets, DisconnectReason, Room, RoomEvent, Track, VideoPreset } from "livekit-client";
export { EpochKeyProvider } from "./keyProvider";

/** SDK-level resume/reconnect: steady 2–3 s retries instead of the default
 *  7 s gaps (~35 s total), then the engine's own token rejoin takes over. */
export const reconnectPolicy = new DefaultReconnectPolicy([
  0, 300, 1000, 1500, 2000, 2000, 2000, 2000, 2000, 2000, 3000, 3000, 3000, 3000, 3000,
]);

/**
 * WebView2 has a hardware H.264 encoder for Baseline/Main/High but
 * only software (OpenH264) for Constrained Baseline (42e0xx), which LiveKit
 * would negotiate first. Dropping it from every video transceiver's codec
 * preferences makes the SFU answer with High → hardware encoding.
 *
 * Only with a hardware encoder: without one this removes the sole
 * profile OpenH264 can encode and LiveKit accepts. The stream then falls back
 * to VP8, and viewers wait forever. Until the probe answers, nothing changes.
 */
function preferHardwareH264(): void {
  if (typeof RTCPeerConnection === "undefined" || typeof RTCRtpSender === "undefined") return;
  const proto = RTCPeerConnection.prototype as RTCPeerConnection & { __lvH264?: true };
  if (proto.__lvH264) return;
  proto.__lvH264 = true;
  let hardware = false;
  void navigator.mediaCapabilities
    ?.encodingInfo({
      type: "webrtc",
      video: {
        contentType: "video/H264;profile-level-id=64001f;packetization-mode=1",
        width: 1920,
        height: 1080,
        bitrate: 5_000_000,
        framerate: 30,
      },
    })
    .then((r) => (hardware = r.powerEfficient))
    .catch(() => {});
  const orig = proto.addTransceiver;
  proto.addTransceiver = function (
    this: RTCPeerConnection,
    ...args: Parameters<RTCPeerConnection["addTransceiver"]>
  ) {
    const t = orig.apply(this, args);
    const kind = typeof args[0] === "string" ? args[0] : args[0].kind;
    const caps = kind === "video" ? RTCRtpSender.getCapabilities("video")?.codecs : undefined;
    if (hardware && caps && typeof t.setCodecPreferences === "function") {
      try {
        t.setCodecPreferences(
          caps.filter(
            (c) => !(c.mimeType === "video/H264" && /profile-level-id=42e0/i.test(c.sdpFmtpLine ?? "")),
          ),
        );
      } catch {
        // keep the browser defaults
      }
    }
    return t;
  };
}
preferHardwareH264();

export function e2eeWorker(): Worker {
  return new E2EEWorker();
}
