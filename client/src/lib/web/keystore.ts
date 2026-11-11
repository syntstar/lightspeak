// Device key storage for the browser client: one X25519 key pair
// per server origin in IndexedDB. The private key is a non-extractable
// CryptoKey – scripts can use it to open envelopes but never read its bytes.

const DB = "lightspeak";
const STORE = "deviceKeys";

export interface StoredKey {
  privateKey: CryptoKey;
  /** Raw public key, base64. */
  publicKey: string;
}

function db(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB, 1);
    req.onupgradeneeded = () => req.result.createObjectStore(STORE);
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error ?? new Error("indexeddb"));
    req.onblocked = () => reject(new Error("indexeddb_blocked"));
  });
}

async function tx<T>(mode: IDBTransactionMode, run: (s: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  const d = await db();
  try {
    return await new Promise<T>((resolve, reject) => {
      const t = d.transaction(STORE, mode);
      const req = run(t.objectStore(STORE));
      t.oncomplete = () => resolve(req.result);
      t.onerror = () => reject(t.error ?? new Error("indexeddb"));
      t.onabort = () => reject(t.error ?? new Error("indexeddb"));
    });
  } finally {
    d.close();
  }
}

export async function loadKey(server: string): Promise<StoredKey | null> {
  const v = (await tx("readonly", (s) => s.get(server))) as StoredKey | undefined;
  return v && v.privateKey instanceof CryptoKey && typeof v.publicKey === "string" ? v : null;
}

export async function saveKey(server: string, key: StoredKey): Promise<void> {
  await tx("readwrite", (s) => s.put(key, server));
}

export async function deleteKey(server: string): Promise<void> {
  await tx("readwrite", (s) => s.delete(server));
}

/** Asks the browser not to evict our storage under pressure (best effort). */
export function persistStorage(): void {
  void navigator.storage?.persist?.().catch(() => false);
}
