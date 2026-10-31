// Device-local recovery copies (IndexedDB). They supplement DB autosave and
// never count as "saved". Records are keyed by tab session + local draft ID so
// two tabs never overwrite or delete each other's copies.
import type { Doc } from "./articleSession.ts";

export const RECOVERY_SCHEMA = 1;
export const RECOVERY_RETENTION_MS = 30 * 24 * 60 * 60 * 1000;

export type PendingImageRecord = {
  clientImageId: string;
  requestId: string;
  name: string;
  type: string;
  blob: Blob;
};

export type RecoveryRecord = {
  schema: number;
  key: string;
  tabSessionId: string;
  localId: string;
  articleId: string | null;
  creationKey: string;
  initialPayload: Doc | null;
  base: { version: number; doc: Doc } | null;
  doc: Doc;
  pendingImages: PendingImageRecord[];
  updatedAt: number;
};

export interface RecoveryStore {
  put(record: RecoveryRecord): Promise<void>;
  delete(key: string): Promise<void>;
  list(): Promise<RecoveryRecord[]>;
  clear(): Promise<void>;
}

export class MemoryRecoveryStore implements RecoveryStore {
  records = new Map<string, RecoveryRecord>();
  failWrites = false;
  async put(record: RecoveryRecord) {
    if (this.failWrites) throw new DOMException("quota", "QuotaExceededError");
    this.records.set(record.key, structuredClone(record));
  }
  async delete(key: string) {
    this.records.delete(key);
  }
  async list() {
    return [...this.records.values()].map((record) => structuredClone(record));
  }
  async clear() {
    this.records.clear();
  }
}

const DB_NAME = "course-k-blog-admin";
const STORE = "drafts";

function promisify<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

export class IndexedDbRecoveryStore implements RecoveryStore {
  private db: Promise<IDBDatabase> | null = null;
  private open() {
    this.db ??= new Promise((resolve, reject) => {
      const request = indexedDB.open(DB_NAME, RECOVERY_SCHEMA);
      request.onupgradeneeded = () => {
        if (!request.result.objectStoreNames.contains(STORE))
          request.result.createObjectStore(STORE, { keyPath: "key" });
      };
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
      request.onblocked = () => reject(new Error("recovery storage blocked"));
    });
    this.db.catch(() => {
      this.db = null;
    });
    return this.db;
  }
  private async transaction<T>(mode: IDBTransactionMode, run: (store: IDBObjectStore) => IDBRequest<T>) {
    const db = await this.open();
    const tx = db.transaction(STORE, mode);
    const result = promisify(run(tx.objectStore(STORE)));
    await new Promise<void>((resolve, reject) => {
      tx.oncomplete = () => resolve();
      tx.onerror = () => reject(tx.error);
      tx.onabort = () => reject(tx.error ?? new Error("recovery write aborted"));
    });
    return result;
  }
  async put(record: RecoveryRecord) {
    // One transaction replaces the record, including the local → DB ID mapping.
    await this.transaction("readwrite", (store) => store.put(record));
  }
  async delete(key: string) {
    await this.transaction("readwrite", (store) => store.delete(key));
  }
  async list() {
    return (await this.transaction("readonly", (store) => store.getAll())) as RecoveryRecord[];
  }
  async clear() {
    await this.transaction("readwrite", (store) => store.clear());
  }
}

export function createRecoveryStore(): RecoveryStore | null {
  try {
    return typeof indexedDB === "undefined" ? null : new IndexedDbRecoveryStore();
  } catch {
    return null;
  }
}

export function newLocalId() {
  return crypto.randomUUID();
}

/** One ID per page load; held as a Web Lock so other tabs can tell it is alive. */
export const tabSessionId = typeof crypto !== "undefined" ? crypto.randomUUID() : "server";
let lockHeld = false;
export function holdTabLock() {
  if (lockHeld || typeof navigator === "undefined" || !navigator.locks) return;
  lockHeld = true;
  void navigator.locks
    .request(`course-k-blog-tab-${tabSessionId}`, () => new Promise<void>(() => {}))
    .catch(() => {
      lockHeld = false;
    });
}
export async function liveTabSessions(): Promise<Set<string> | null> {
  if (typeof navigator === "undefined" || !navigator.locks) return null;
  try {
    const state = await navigator.locks.query();
    return new Set(
      (state.held ?? [])
        .map((lock) => lock.name ?? "")
        .filter((name) => name.startsWith("course-k-blog-tab-"))
        .map((name) => name.slice("course-k-blog-tab-".length)),
    );
  } catch {
    return null;
  }
}

/** Removes records older than 30 days and returns the rest, newest first,
 * excluding records owned by tabs that are still open. */
export async function recoverableRecords(
  store: RecoveryStore,
  now: number,
  live: Set<string> | null,
): Promise<RecoveryRecord[]> {
  const records = await store.list();
  const keep: RecoveryRecord[] = [];
  for (const record of records) {
    if (record.schema !== RECOVERY_SCHEMA || now - record.updatedAt > RECOVERY_RETENTION_MS) {
      await store.delete(record.key);
      continue;
    }
    if (record.tabSessionId === tabSessionId) continue;
    if (live?.has(record.tabSessionId)) continue;
    keep.push(record);
  }
  return keep.sort((a, b) => b.updatedAt - a.updatedAt);
}

/** Debounces writes: 250 ms after the last change, at most 1 s apart while
 * typing continues, and immediately on demand (page hidden). */
export class RecoveryWriter {
  private timer: ReturnType<typeof setTimeout> | null = null;
  private firstPendingAt: number | null = null;
  private pending: (() => RecoveryRecord | null) | null = null;
  private store: RecoveryStore;
  private onUnavailable: () => void;

  constructor(store: RecoveryStore, onUnavailable: () => void) {
    this.store = store;
    this.onUnavailable = onUnavailable;
  }

  schedule(build: () => RecoveryRecord | null, now = Date.now()) {
    this.pending = build;
    this.firstPendingAt ??= now;
    const wait = Math.max(0, Math.min(250, this.firstPendingAt + 1000 - now));
    if (this.timer) clearTimeout(this.timer);
    this.timer = setTimeout(() => void this.writeNow(), wait);
  }

  async writeNow() {
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    this.firstPendingAt = null;
    const build = this.pending;
    this.pending = null;
    const record = build?.();
    if (!record) return;
    try {
      await this.store.put(record);
    } catch {
      this.onUnavailable();
    }
  }

  async remove(key: string) {
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    this.pending = null;
    this.firstPendingAt = null;
    try {
      await this.store.delete(key);
    } catch {
      this.onUnavailable();
    }
  }
}
