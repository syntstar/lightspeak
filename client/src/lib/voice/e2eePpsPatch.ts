// Build-time patch of livekit-client's E2EE worker.
//
// Chrome reads pic_parameter_set_id from an H.264 IDR slice BEFORE the
// decrypt transform runs and drops the keyframe if the id names an unknown
// PPS ("No PPS with id << N received"). The worker leaves only the NALU
// header + 1 byte unencrypted; with encoders that code slice_type in 7 bits
// (AMD/Intel Media Foundation) the PPS id lies in the encrypted part, so
// every IDR is a coin toss. Lost keyframes stall the stream for seconds after
// layer switches and rejoins (livekit client-sdk-js #2180).
//
// Upstream fix #2181 (merged, not yet released): encrypt the IDR again with a
// fresh IV until the sent bits still read as the true PPS id. The receiver
// takes the IV from the frame trailer, so the wire format is unchanged and
// only the sender needs the patch. This file ports that fix to the shipped
// worker bundle. Once livekit ships it, the patch steps aside by itself.

const MARKER = "parseH264IdrPpsId";

/** Injected helper, same logic as upstream `parseH264IdrPpsId` (uses the bundle's parseRbsp). */
const HELPER = `
const MAX_PPS_ID_ENCRYPT_ATTEMPTS = 16;
function ${MARKER}(nalu) {
  if (nalu.length < 1 || (nalu[0] & 0x1f) !== 5) return null;
  const rbsp = parseRbsp(nalu.subarray(1, 17));
  const bitLength = rbsp.length * 8;
  let bit = 0;
  const readBit = () => (rbsp[bit >> 3] >> (7 - (bit++ & 7))) & 1;
  const readUe = () => {
    let zeros = 0;
    while (readBit() === 0 && bit < bitLength) zeros++;
    let value = 1;
    for (let i = 0; i < zeros; i++) value = value * 2 + readBit();
    return value - 1;
  };
  readUe();
  readUe();
  const ppsId = readUe();
  return bit <= bitLength ? ppsId : null;
}
`;

const ENCRYPT_OLD = `const cipherText = yield crypto.subtle.encrypt({
            name: ENCRYPTION_ALGORITHM,
            iv,
            additionalData: frameHeader
          }, encryptionKey, new Uint8Array(encodedFrame.data, frameInfo.unencryptedBytes));`;

const ENCRYPT_NEW = `const sliceStart = frameInfo.unencryptedBytes - 2;
          const ppsId = frameInfo.requiresNALUProcessing ? ${MARKER}(new Uint8Array(encodedFrame.data, sliceStart)) : null;
          let cipherText;
          for (let attempt = 1;; attempt++) {
            cipherText = yield crypto.subtle.encrypt({
              name: ENCRYPTION_ALGORITHM,
              iv,
              additionalData: frameHeader
            }, encryptionKey, new Uint8Array(encodedFrame.data, frameInfo.unencryptedBytes));
            if (ppsId === null || attempt === MAX_PPS_ID_ENCRYPT_ATTEMPTS) break;
            const escapedStart = writeRbsp(new Uint8Array(cipherText, 0, Math.min(16, cipherText.byteLength)));
            const sentSlice = new Uint8Array(2 + escapedStart.byteLength);
            sentSlice.set(new Uint8Array(encodedFrame.data, sliceStart, 2));
            sentSlice.set(escapedStart, 2);
            if (${MARKER}(sentSlice) === ppsId) break;
            iv = this.makeIV((_a = encodedFrame.getMetadata().synchronizationSource) !== null && _a !== void 0 ? _a : -1, encodedFrame.timestamp);
          }`;

const IV_OLD = `const iv = this.makeIV((_a = encodedFrame.getMetadata().synchronizationSource) !== null && _a !== void 0 ? _a : -1, encodedFrame.timestamp);
        let frameInfo = this.getUnencryptedBytes(encodedFrame);`;
const IV_NEW = IV_OLD.replace("const iv =", "let iv =");

const ANCHOR = "function writeRbsp(data_in) {";

function replaceOnce(code: string, from: string, to: string, what: string): string {
  const at = code.indexOf(from);
  if (at < 0 || code.indexOf(from, at + 1) >= 0)
    throw new Error(
      `e2ee PPS patch: pattern "${what}" not found exactly once – livekit-client changed, review the patch`,
    );
  return code.slice(0, at) + to + code.slice(at + from.length);
}

/** Patches the E2EE worker bundle; returns it unchanged if livekit already has the fix. */
export function patchE2eeWorker(code: string): string {
  if (code.includes(MARKER)) return code;
  const src = code.replace(/\r\n/g, "\n");
  let out = replaceOnce(src, ANCHOR, HELPER + ANCHOR, "writeRbsp");
  out = replaceOnce(out, IV_OLD, IV_NEW, "makeIV");
  out = replaceOnce(out, ENCRYPT_OLD, ENCRYPT_NEW, "encrypt");
  return out;
}
