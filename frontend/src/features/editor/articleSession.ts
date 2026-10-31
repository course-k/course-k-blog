// Autosave for one article. Framework-agnostic so timing, retries and
// unknown outcomes can be tested with a fake clock.
//
// - Save starts 3 s after the last edit, or 30 s after the article first became
//   unsaved, whichever is earlier. Further typing never extends the 30 s limit.
// - One request per article is in flight; the newest content waits in a single slot.
// - A response only acknowledges the snapshot it was sent with.
// - Unknown outcomes are verified by reading before anything is resent.
import type { AdminPost } from "../../lib/types";
import { ApiError, outcomeIsUnknown } from "./adminApi.ts";
import { removePending } from "./imageMarkdown.ts";

export type Doc = { title: string; markdown: string };
export type SessionApi = {
  create(doc: Doc, creationKey: string): Promise<AdminPost>;
  save(id: string, doc: Doc, version: number): Promise<AdminPost>;
  get(id: string): Promise<AdminPost>;
  findCreation(creationKey: string): Promise<AdminPost>;
  publish(id: string, version: number): Promise<AdminPost>;
  unpublish(id: string, version: number): Promise<AdminPost>;
};
export type Clock = {
  now(): number;
  setTimeout(callback: () => void, ms: number): unknown;
  clearTimeout(handle: unknown): void;
};
export const systemClock: Clock = {
  now: () => Date.now(),
  setTimeout: (callback, ms) => globalThis.setTimeout(callback, ms),
  clearTimeout: (handle) =>
    globalThis.clearTimeout(handle as ReturnType<typeof setTimeout>),
};

export const IDLE_DELAY_MS = 3_000;
export const MAX_UNSAVED_MS = 30_000;
const RETRY_DELAYS_MS = [5_000, 10_000, 30_000, 60_000];

export type SaveState =
  | "none" // never created and empty: nothing to send
  | "dirty"
  | "saving"
  | "saved"
  | "retrying" // transport failure, automatic retry scheduled
  | "verifying" // outcome unknown, reading the server to compare
  | "auth" // session expired; input kept, sending stopped
  | "conflict" // the server copy changed elsewhere
  | "invalid" // the server rejected the input
  | "forbidden"
  | "missing";

type Flight =
  | { kind: "create"; snapshot: Doc; dirtySince: number | null }
  | { kind: "save"; snapshot: Doc; baseVersion: number; base: Doc; dirtySince: number | null }
  | { kind: "publish" | "unpublish"; baseVersion: number }
  | { kind: "verify" };

type Operation = {
  kind: "publish" | "unpublish";
  resolve: () => void;
  reject: (error: Error) => void;
};

export type SessionSnapshot = {
  id: string | null;
  doc: Doc;
  version: number | null;
  published: boolean;
  publishedAt: string | null;
  serverHasUnpublishedChanges: boolean;
  save: SaveState;
  message: string;
  operation: "publish" | "unpublish" | null;
  operationMessage: string;
  conflictServer: AdminPost | null;
  retryAt: number | null;
  dirty: boolean;
};

export type SessionInit = {
  post?: AdminPost | null;
  doc?: Doc;
  creationKey?: string;
  initialPayload?: Doc | null;
  /** Server copy the restored local edits were based on. */
  base?: { version: number; doc: Doc } | null;
};

export type SessionHooks = {
  /** Called with the fixed first payload before the creation request is sent. */
  beforeCreate?: (creationKey: string, payload: Doc) => Promise<void> | void;
  onCreated?: (post: AdminPost) => void;
  onAuthExpired?: () => void;
};

export class UnknownOutcomeError extends Error {}

/** Version of an article immediately after creation (posts.version default). */
const CREATED_VERSION = 1;

const sameDoc = (a: Doc | null, b: Doc | null) =>
  !!a && !!b && a.title === b.title && a.markdown === b.markdown;

export function newCreationKey() {
  const bytes = crypto.getRandomValues(new Uint8Array(32));
  return btoa(String.fromCharCode(...bytes))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

export function retryDelay(attempt: number) {
  return RETRY_DELAYS_MS[Math.min(Math.max(attempt, 1), RETRY_DELAYS_MS.length) - 1];
}

export class ArticleSession {
  readonly creationKey: string;
  private id: string | null = null;
  private version: number | null = null;
  private published = false;
  private publishedAt: string | null = null;
  private serverHasUnpublishedChanges = false;
  private live: Doc;
  /** Server-confirmed working copy (pending image slots removed). */
  private ack: Doc | null = null;
  private initialPayload: Doc | null = null;
  private dirtySince: number | null = null;
  private lastEditAt = 0;
  private composing = false;
  private inFlight: Flight | null = null;
  private phase: "idle" | "retry" | "unknown" | SaveState = "idle";
  private unknownFlight: Flight | null = null;
  private retrySnapshot: Doc | null = null;
  private retryAction: "send" | "verify" = "send";
  private retryAt: number | null = null;
  private attempt = 0;
  private message = "";
  private operationMessage = "";
  private conflictServer: AdminPost | null = null;
  private timer: unknown = null;
  private flushWaiters: ((saved: boolean) => void)[] = [];
  private flushRequested = false;
  private operation: Operation | null = null;
  private runningOperation: "publish" | "unpublish" | null = null;
  private listeners = new Set<() => void>();
  private snapshotCache: SessionSnapshot | null = null;
  private disposed = false;

  private readonly api: SessionApi;
  private readonly hooks: SessionHooks;
  private readonly clock: Clock;

  constructor(
    api: SessionApi,
    init: SessionInit = {},
    hooks: SessionHooks = {},
    clock: Clock = systemClock,
  ) {
    this.api = api;
    this.hooks = hooks;
    this.clock = clock;
    this.creationKey = init.creationKey ?? newCreationKey();
    this.initialPayload = init.initialPayload ?? null;
    if (init.post) this.applyServer(init.post, true);
    if (init.base) {
      this.version = init.base.version;
      this.ack = init.base.doc;
    }
    this.live = init.doc ?? this.ack ?? { title: "", markdown: "" };
    if (this.needsSave()) {
      this.dirtySince = this.clock.now();
      this.lastEditAt = this.clock.now();
      this.pump();
    }
  }

  // ---- observation -------------------------------------------------------
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };
  getSnapshot = (): SessionSnapshot => {
    if (!this.snapshotCache) this.snapshotCache = this.computeSnapshot();
    return this.snapshotCache;
  };
  private emit() {
    this.snapshotCache = null;
    for (const listener of this.listeners) listener();
  }
  private computeSnapshot(): SessionSnapshot {
    const dirty = this.needsSave();
    let save: SaveState;
    if (this.phase === "unknown") save = "verifying";
    else if (this.phase === "retry") save = "retrying";
    else if (this.phase !== "idle") save = this.phase;
    else if (this.inFlight?.kind === "create" || this.inFlight?.kind === "save")
      save = "saving";
    else if (this.inFlight?.kind === "verify") save = "verifying";
    else if (dirty) save = "dirty";
    else if (this.ack) save = "saved";
    else save = "none";
    return {
      id: this.id,
      doc: this.live,
      version: this.version,
      published: this.published,
      publishedAt: this.publishedAt,
      serverHasUnpublishedChanges: this.serverHasUnpublishedChanges,
      save,
      message: this.message,
      operation: this.runningOperation ?? this.operation?.kind ?? null,
      operationMessage: this.operationMessage,
      conflictServer: this.conflictServer,
      retryAt: this.retryAt,
      dirty,
    };
  }

  /** Data the recovery store keeps for this session. */
  recoveryState() {
    return {
      articleId: this.id,
      creationKey: this.creationKey,
      initialPayload: this.initialPayload,
      base: this.ack && this.version !== null ? { version: this.version, doc: this.ack } : null,
      doc: this.live,
      clean: !this.hasUnsavedWork(),
    };
  }

  hasUnsavedWork() {
    return (
      this.needsSave() ||
      this.inFlight !== null ||
      this.phase !== "idle" ||
      this.operation !== null
    );
  }

  // ---- input -------------------------------------------------------------
  setDocument(doc: Doc) {
    if (this.disposed || sameDoc(doc, this.live)) return;
    const now = this.clock.now();
    this.live = doc;
    this.lastEditAt = now;
    if (this.phase === "invalid") {
      this.phase = "idle";
      this.message = "";
    }
    // Edits made while a request is in flight are compared with what it carries.
    const unsaved =
      this.inFlight && "snapshot" in this.inFlight
        ? !sameDoc(this.payload(), this.inFlight.snapshot)
        : this.needsSave();
    if (!unsaved && !this.inFlight) this.dirtySince = null;
    else if (unsaved && this.dirtySince === null) this.dirtySince = now;
    this.emit();
    this.pump();
  }

  /** While an IME composes, nothing is sent; the deadline is re-evaluated after. */
  setComposing(composing: boolean) {
    this.composing = composing;
    if (!composing) this.pump();
  }

  // ---- explicit actions --------------------------------------------------
  /** Saves now. Resolves true once the latest content is acknowledged. */
  flush(): Promise<boolean> {
    if (this.disposed) return Promise.resolve(!this.hasUnsavedWork());
    if (this.phase === "retry") {
      this.phase = this.retryAction === "verify" ? "unknown" : "idle";
      this.retryAt = null;
    }
    return new Promise((resolve) => {
      this.flushWaiters.push(resolve);
      this.flushRequested = true;
      this.pump();
    });
  }

  /** Manual "再試行" and the browser's online event. */
  retryNow() {
    if (this.phase === "retry") {
      this.phase = this.retryAction === "verify" ? "unknown" : "idle";
      this.retryAt = null;
      this.emit();
      this.pump();
    }
  }

  publish(): Promise<void> {
    return this.queueOperation("publish");
  }
  unpublish(): Promise<void> {
    return this.queueOperation("unpublish");
  }

  /** After a conflict, explicitly save the local text over the newer server copy. */
  overwriteWithLocal() {
    if (this.phase !== "conflict" || !this.conflictServer) return;
    this.applyServer(this.conflictServer, true);
    this.conflictServer = null;
    this.phase = "idle";
    this.message = "";
    this.dirtySince = this.dirtySince ?? this.clock.now();
    this.flushRequested = true;
    this.emit();
    this.pump();
  }

  /** After a conflict, discard local text and continue from the server copy. */
  adoptServer() {
    if (this.phase !== "conflict" || !this.conflictServer) return;
    this.applyServer(this.conflictServer, true);
    this.live = { ...this.ack! };
    this.conflictServer = null;
    this.phase = "idle";
    this.message = "";
    this.dirtySince = null;
    this.emit();
  }

  /** After re-login, resume sending. */
  resumeAfterLogin() {
    if (this.phase !== "auth") return;
    this.phase = this.unknownFlight ? "unknown" : "idle";
    this.message = "";
    this.emit();
    this.pump();
  }

  dispose() {
    this.disposed = true;
    this.clearTimer();
    this.resolveWaiters(!this.hasUnsavedWork());
    this.listeners.clear();
  }

  // ---- internals ---------------------------------------------------------
  private payload(doc: Doc = this.live): Doc {
    return { title: doc.title, markdown: removePending(doc.markdown) };
  }
  private isEmpty(doc: Doc) {
    return doc.title === "" && doc.markdown === "";
  }
  private needsSave() {
    if (!this.id && this.ack === null)
      return this.initialPayload !== null || !this.isEmpty(this.payload());
    return !sameDoc(this.payload(), this.ack);
  }
  private applyServer(post: AdminPost, adoptAsAck: boolean) {
    this.id = post.id;
    this.version = post.version;
    this.published = post.published;
    this.publishedAt = post.publishedAt;
    this.serverHasUnpublishedChanges = post.hasUnpublishedChanges;
    if (adoptAsAck) this.ack = { title: post.title, markdown: post.markdown };
  }
  private clearTimer() {
    if (this.timer !== null) this.clock.clearTimeout(this.timer);
    this.timer = null;
  }
  private schedule(at: number) {
    this.clearTimer();
    this.timer = this.clock.setTimeout(
      () => {
        this.timer = null;
        if (this.phase === "retry") {
          this.phase = this.retryAction === "verify" ? "unknown" : "idle";
          this.retryAt = null;
          this.emit();
        }
        this.pump();
      },
      Math.max(0, at - this.clock.now()),
    );
  }
  private resolveWaiters(saved: boolean) {
    const waiters = this.flushWaiters;
    this.flushWaiters = [];
    this.flushRequested = false;
    for (const resolve of waiters) resolve(saved);
  }
  private failPending(error: Error) {
    this.resolveWaiters(false);
    const operation = this.operation;
    this.operation = null;
    operation?.reject(error);
  }

  private pump() {
    if (this.disposed || this.inFlight) return;
    this.clearTimer();
    if (this.phase === "retry") {
      if (this.flushWaiters.length || this.operation)
        this.failPending(new Error(this.message || "保存できませんでした。"));
      if (this.retryAt !== null) this.schedule(this.retryAt);
      return;
    }
    if (this.phase === "unknown") {
      void this.verify();
      return;
    }
    if (this.phase !== "idle") {
      this.failPending(new Error(this.message || "保存できませんでした。"));
      return;
    }
    if (this.needsSave()) {
      const urgent = this.flushRequested || this.operation !== null || this.retrySnapshot !== null;
      if (urgent) {
        void this.send();
        return;
      }
      if (this.composing) return;
      const due = Math.min(
        this.lastEditAt + IDLE_DELAY_MS,
        (this.dirtySince ?? this.lastEditAt) + MAX_UNSAVED_MS,
      );
      if (due <= this.clock.now()) void this.send();
      else this.schedule(due);
      return;
    }
    this.resolveWaiters(true);
    if (this.operation) void this.runOperation();
  }

  private async send() {
    let flight: Flight;
    if (!this.id) {
      if (!this.initialPayload) {
        this.initialPayload = this.payload();
        try {
          await this.hooks.beforeCreate?.(this.creationKey, this.initialPayload);
        } catch {
          /* recovery storage is best effort; DB autosave continues */
        }
        if (this.disposed || this.inFlight) return;
      }
      flight = { kind: "create", snapshot: this.initialPayload, dirtySince: this.dirtySince };
    } else {
      const snapshot = this.retrySnapshot ?? this.payload();
      flight = {
        kind: "save",
        snapshot,
        baseVersion: this.version!,
        base: this.ack!,
        dirtySince: this.dirtySince,
      };
    }
    this.retrySnapshot = null;
    if (sameDoc(this.payload(), flight.snapshot)) this.dirtySince = null;
    this.inFlight = flight;
    this.emit();
    try {
      const post =
        flight.kind === "create"
          ? await this.api.create(flight.snapshot, this.creationKey)
          : await this.api.save(this.id!, flight.snapshot, flight.baseVersion);
      if (this.disposed) return;
      this.acknowledge(post, flight.snapshot, flight.kind === "create");
    } catch (error) {
      if (this.disposed) return;
      this.fail(error, flight);
    } finally {
      this.inFlight = null;
      if (!this.disposed) {
        this.emit();
        this.pump();
      }
    }
  }

  private acknowledge(post: AdminPost, snapshot: Doc, created: boolean) {
    if (created && (post.version !== CREATED_VERSION || !sameDoc({ title: post.title, markdown: post.markdown }, snapshot))) {
      // The key resolved to an article that changed after it was created (e.g.
      // edited in another tab before this copy was restored). Its current
      // version is not permission to overwrite: stop as a conflict.
      this.applyServer(post, true);
      this.attempt = 0;
      this.unknownFlight = null;
      this.phase = "conflict";
      this.message = "保存先が別の画面で更新されています。";
      this.conflictServer = post;
      this.hooks.onCreated?.(post);
      return;
    }
    this.applyServer(post, false);
    this.ack = snapshot;
    this.attempt = 0;
    this.phase = "idle";
    this.message = "";
    this.unknownFlight = null;
    if (!this.needsSave()) this.dirtySince = null;
    if (created) this.hooks.onCreated?.(post);
  }

  private fail(error: unknown, flight: Flight) {
    if ("dirtySince" in flight && flight.dirtySince !== null)
      this.dirtySince = Math.min(flight.dirtySince, this.dirtySince ?? Infinity);
    const status = error instanceof ApiError ? error.status : 0;
    if (outcomeIsUnknown(error)) {
      this.unknownFlight = flight;
      this.phase = "unknown";
      this.message = "";
      return;
    }
    this.message = error instanceof Error ? error.message : "保存できませんでした。";
    if (status === 401) {
      this.phase = "auth";
      this.message = "再ログインが必要です。入力は保持しています。";
      this.hooks.onAuthExpired?.();
    } else if (status === 409) {
      this.phase = "conflict";
      this.message = "保存先が別の画面で更新されています。";
      void this.loadConflict();
    } else if (status === 429) {
      const seconds = error instanceof ApiError && error.retryAfter ? error.retryAfter : 60;
      this.phase = "retry";
      this.retryAction = "send";
      if (flight.kind === "save") this.retrySnapshot = flight.snapshot;
      this.retryAt = this.clock.now() + seconds * 1000;
    } else if (status === 403) this.phase = "forbidden";
    else if (status === 404) this.phase = "missing";
    else {
      this.phase = "invalid";
      // Rejected before anything was stored: the fixed first payload no longer
      // binds the key, so the corrected text is sent on the next attempt.
      if (flight.kind === "create" && status === 400) this.initialPayload = null;
    }
  }

  private async loadConflict() {
    if (!this.id) return;
    try {
      const server = await this.api.get(this.id);
      if (!this.disposed && this.phase === "conflict") {
        this.conflictServer = server;
        this.emit();
      }
    } catch {
      /* the conflict notice still offers copying local text */
    }
  }

  /** Waits 5 s, 10 s, 30 s, then every 60 s across consecutive failures. */
  private scheduleRetry(action: "send" | "verify", delay?: number) {
    this.attempt++;
    this.phase = "retry";
    this.retryAction = action;
    this.retryAt = this.clock.now() + (delay ?? retryDelay(this.attempt));
    this.message = "保存できませんでした。再試行します。";
  }

  /** Reads the server to decide whether an unknown write committed. */
  private async verify() {
    const flight = this.unknownFlight;
    if (!flight) {
      this.phase = "idle";
      this.pump();
      return;
    }
    this.inFlight = { kind: "verify" };
    this.emit();
    try {
      if (flight.kind === "create") {
        try {
          const post = await this.api.findCreation(this.creationKey);
          if (this.disposed) return;
          this.acknowledge(post, flight.snapshot, true);
        } catch (error) {
          if (!(error instanceof ApiError && error.status === 404)) throw error;
          // Not created: resend the same key with the same first payload.
          this.unknownFlight = null;
          this.scheduleRetry("send");
        }
      } else if (flight.kind === "save") {
        const server = await this.api.get(this.id!);
        if (this.disposed) return;
        const serverDoc = { title: server.title, markdown: server.markdown };
        if (server.version > flight.baseVersion && sameDoc(serverDoc, flight.snapshot)) {
          this.acknowledge(server, flight.snapshot, false);
        } else if (server.version === flight.baseVersion && sameDoc(serverDoc, flight.base)) {
          this.unknownFlight = null;
          this.retrySnapshot = flight.snapshot;
          this.scheduleRetry("send");
        } else {
          this.unknownFlight = null;
          this.phase = "conflict";
          this.message = "保存先が別の画面で更新されています。";
          this.conflictServer = server;
        }
      } else if (flight.kind === "publish" || flight.kind === "unpublish") {
        // Never repeat publication automatically; report what the server shows.
        const server = await this.api.get(this.id!);
        if (this.disposed) return;
        this.unknownFlight = null;
        this.phase = "idle";
        this.message = "";
        if (sameDoc({ title: server.title, markdown: server.markdown }, this.ack)) {
          this.applyServer(server, false);
          this.operationMessage = server.published
            ? "公開状態を確認しました。現在は公開中です。"
            : "公開状態を確認しました。現在は公開されていません。";
        } else {
          this.phase = "conflict";
          this.message = "保存先が別の画面で更新されています。";
          this.conflictServer = server;
        }
      }
    } catch (error) {
      if (this.disposed) return;
      const status = error instanceof ApiError ? error.status : 0;
      // The outcome stays unknown (unknownFlight is kept) whatever the reason.
      if (status === 401) {
        this.phase = "auth";
        this.message = "再ログインが必要です。入力は保持しています。";
        this.hooks.onAuthExpired?.();
      } else if (status === 403) {
        this.phase = "forbidden";
        this.message = "保存結果を確認できません。権限を確認してください。";
      } else if (status === 404) {
        this.phase = "missing";
        this.message = "記事が見つかりません。";
      } else if (status === 429) {
        const seconds = error instanceof ApiError && error.retryAfter ? error.retryAfter : 60;
        this.scheduleRetry("verify", seconds * 1000);
      } else {
        this.scheduleRetry("verify");
      }
    } finally {
      this.inFlight = null;
      if (!this.disposed) {
        this.emit();
        this.pump();
      }
    }
  }

  private queueOperation(kind: "publish" | "unpublish"): Promise<void> {
    if (this.operation || this.runningOperation)
      return Promise.reject(new Error("公開状態を変更しています。"));
    if (kind === "publish" && this.live.title.trim() === "")
      return Promise.reject(new Error("公開するにはタイトルを入力してください。"));
    if (kind === "unpublish" && !this.id)
      return Promise.reject(new Error("記事が保存されていません。"));
    if (this.phase === "retry") {
      this.phase = this.retryAction === "verify" ? "unknown" : "idle";
      this.retryAt = null;
    }
    this.operationMessage = "";
    return new Promise((resolve, reject) => {
      this.operation = { kind, resolve, reject };
      this.emit();
      this.pump();
    });
  }

  private async runOperation() {
    const operation = this.operation!;
    this.operation = null;
    if (!this.id || this.version === null) {
      operation.reject(new Error("記事が保存されていません。"));
      return;
    }
    const flight: Flight = { kind: operation.kind, baseVersion: this.version };
    this.inFlight = flight;
    this.runningOperation = operation.kind;
    this.emit();
    try {
      const post =
        operation.kind === "publish"
          ? await this.api.publish(this.id, this.version)
          : await this.api.unpublish(this.id, this.version);
      this.applyServer(post, false);
      this.operationMessage =
        operation.kind === "publish" ? "公開しました。" : "非公開にしました。";
      operation.resolve();
    } catch (error) {
      if (outcomeIsUnknown(error)) {
        this.unknownFlight = flight;
        this.phase = "unknown";
        operation.reject(
          new UnknownOutcomeError(
            "公開状態の変更結果を確認できません。サーバーの状態を確認しています。",
          ),
        );
      } else {
        const status = error instanceof ApiError ? error.status : 0;
        if (status === 401) {
          this.phase = "auth";
          this.message = "再ログインが必要です。入力は保持しています。";
          this.hooks.onAuthExpired?.();
        } else if (status === 409) {
          this.phase = "conflict";
          this.message = "保存先が別の画面で更新されています。";
          void this.loadConflict();
        }
        operation.reject(error instanceof Error ? error : new Error("処理できませんでした。"));
      }
    } finally {
      this.inFlight = null;
      this.runningOperation = null;
      if (!this.disposed) {
        this.emit();
        this.pump();
      }
    }
  }
}
