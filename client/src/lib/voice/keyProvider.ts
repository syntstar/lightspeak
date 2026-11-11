// E2EE key provider for one container (space or DM): every member shares the
// container's media key; each epoch maps to a key index (epoch mod 16) so a
// rotation can switch keys without tearing down the call.
import { BaseKeyProvider, createKeyMaterialFromBuffer } from "livekit-client";

export const KEYRING_SIZE = 16;

export function keyIndexForEpoch(epoch: number): number {
  return ((epoch % KEYRING_SIZE) + KEYRING_SIZE) % KEYRING_SIZE;
}

export class EpochKeyProvider extends BaseKeyProvider {
  constructor() {
    // Shared key, no auto-ratcheting: a wrong key must fail closed, never "heal".
    super({ sharedKey: true, ratchetWindowSize: 0, failureTolerance: -1, keyringSize: KEYRING_SIZE });
  }

  /** Installs the raw 32-byte media key of `epoch` and makes it the active one. */
  async setEpochKey(raw: Uint8Array, epoch: number): Promise<void> {
    if (raw.byteLength !== 32) throw new Error("e2ee_bad_key");
    const material = await createKeyMaterialFromBuffer(raw.slice().buffer);
    this.onSetEncryptionKey(material, undefined, keyIndexForEpoch(epoch));
  }
}
