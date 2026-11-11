// Can this browser run Lightspeak? Calls are always end-to-end
// encrypted, so the browser needs WebCrypto X25519 for the device key and
// encoded media streams (insertable streams) for frame encryption.
import { supported as x25519 } from "./sealedBox";

export async function browserSupported(): Promise<boolean> {
  if (!window.isSecureContext || typeof RTCPeerConnection === "undefined") return false;
  const encoded =
    "RTCRtpScriptTransform" in window ||
    ("RTCRtpSender" in window && "createEncodedStreams" in RTCRtpSender.prototype);
  return encoded && (await x25519());
}
