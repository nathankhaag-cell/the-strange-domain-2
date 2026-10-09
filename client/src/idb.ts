// A tiny promise wrapper around IndexedDB. One database, a few key-value
// stores. Values are structured-cloned, so CryptoKey objects (including
// non-extractable ones) and Uint8Arrays are stored as they are.

const DB_NAME = "strange-domain";
const DB_VERSION = 1;
export const STORES = ["device", "keypackages", "groups", "plaintext", "prefs"] as const;
export type StoreName = (typeof STORES)[number];

let dbp: Promise<IDBDatabase> | null = null;

function open(): Promise<IDBDatabase> {
  if (!dbp) {
    dbp = new Promise((resolve, reject) => {
      const req = indexedDB.open(DB_NAME, DB_VERSION);
      req.onupgradeneeded = () => {
        for (const s of STORES) {
          if (!req.result.objectStoreNames.contains(s)) req.result.createObjectStore(s);
        }
      };
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error);
    });
  }
  return dbp;
}

function wrap<T>(req: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

export async function get<T>(store: StoreName, key: string): Promise<T | undefined> {
  const db = await open();
  return wrap(db.transaction(store).objectStore(store).get(key)) as Promise<T | undefined>;
}

export async function put(store: StoreName, key: string, value: unknown): Promise<void> {
  const db = await open();
  const tx = db.transaction(store, "readwrite");
  tx.objectStore(store).put(value, key);
  await done(tx);
}

export async function del(store: StoreName, key: string): Promise<void> {
  const db = await open();
  const tx = db.transaction(store, "readwrite");
  tx.objectStore(store).delete(key);
  await done(tx);
}

/** All entries whose key starts with prefix. */
export async function entries<T>(store: StoreName, prefix = ""): Promise<[string, T][]> {
  const db = await open();
  const os = db.transaction(store).objectStore(store);
  const range = prefix ? IDBKeyRange.bound(prefix, prefix + "￿") : undefined;
  const [keys, values] = await Promise.all([wrap(os.getAllKeys(range)), wrap(os.getAll(range))]);
  return keys.map((k, i) => [String(k), values[i] as T]);
}

/** Write several stores atomically. */
export async function putMany(writes: { store: StoreName; key: string; value: unknown | undefined }[]): Promise<void> {
  if (writes.length === 0) return;
  const db = await open();
  const names = Array.from(new Set(writes.map((w) => w.store)));
  const tx = db.transaction(names, "readwrite");
  for (const w of writes) {
    if (w.value === undefined) tx.objectStore(w.store).delete(w.key);
    else tx.objectStore(w.store).put(w.value, w.key);
  }
  await done(tx);
}

export async function clearAll(): Promise<void> {
  const db = await open();
  const tx = db.transaction([...STORES], "readwrite");
  for (const s of STORES) tx.objectStore(s).clear();
  await done(tx);
}

function done(tx: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
    tx.onabort = () => reject(tx.error);
  });
}
