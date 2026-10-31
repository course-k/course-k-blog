// Upload queue for pasted/dropped images: at most two in parallel, each one
// independent. The slot in the text (clientImageId) never changes; the server
// request ID changes only when a 24-hour session expired and the owner retries.
import { ApiError, type UploadRequest, type UploadResult, type UploadStatus, type UploadTarget } from "./adminApi.ts";

export const MAX_IMAGE_BYTES = 10_000_000;
export const ACCEPTED_IMAGE_TYPES = ["image/png", "image/jpeg", "image/webp"];
export const IMAGE_LIMITS_TEXT = "PNG・JPEG・WebP、1枚10MB以下、1200万画素以下の画像を使えます。";

/** Client-side check before inserting a slot; the server verifies the bytes again. */
export function rejectImage(file: { type: string; size: number; name: string }): string | null {
  if (!ACCEPTED_IMAGE_TYPES.includes(file.type))
    return `「${file.name}」は使えない形式です。${IMAGE_LIMITS_TEXT}SVG・GIF・HEICは対象外です。`;
  if (file.size === 0) return `「${file.name}」は0バイトのため使えません。`;
  if (file.size > MAX_IMAGE_BYTES)
    return `「${file.name}」は10MB（10,000,000バイト）を超えています。${IMAGE_LIMITS_TEXT}`;
  return null;
}

export type UploadApi = {
  begin(request: UploadRequest): Promise<UploadStatus>;
  complete(uploadId: string): Promise<UploadStatus>;
  status(uploadId: string): Promise<UploadStatus>;
  send(target: UploadTarget, blob: Blob, signal: AbortSignal): Promise<void>;
  digest(blob: Blob): Promise<string>;
  wait(ms: number): Promise<void>;
};

export type UploadItem = {
  clientImageId: string;
  requestId: string;
  name: string;
  type: string;
  blob: Blob;
  status: "queued" | "uploading" | "done" | "failed";
  expired?: boolean;
  /** Failed because the session expired; resumed after login. */
  authExpired?: boolean;
  error?: string;
  result?: UploadResult;
};

export type UploadHooks = {
  onChange(): void;
  onDone(item: UploadItem, result: UploadResult): void;
  onAuthExpired?(): void;
};

export class TransferError extends Error {
  readonly status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

const SEND_FAILED = "画像を送信できませんでした。";

export class ImageUploadQueue {
  private items = new Map<string, UploadItem>();
  private order: string[] = [];
  private active = new Map<string, AbortController>();
  private api: UploadApi;
  private hooks: UploadHooks;
  private concurrency: number;
  private disposed = false;

  constructor(api: UploadApi, hooks: UploadHooks, concurrency = 2) {
    this.api = api;
    this.hooks = hooks;
    this.concurrency = concurrency;
  }

  list(): UploadItem[] {
    return this.order.map((id) => this.items.get(id)!).filter(Boolean);
  }
  get(clientImageId: string) {
    return this.items.get(clientImageId);
  }
  busy() {
    return this.list().some((item) => item.status === "queued" || item.status === "uploading");
  }
  failed() {
    return this.list().some((item) => item.status === "failed");
  }

  add(entries: { clientImageId: string; requestId?: string; name: string; type: string; blob: Blob }[]) {
    if (this.disposed) return;
    for (const entry of entries) {
      if (this.items.has(entry.clientImageId)) continue;
      this.items.set(entry.clientImageId, {
        ...entry,
        requestId: entry.requestId ?? crypto.randomUUID(),
        status: "queued",
      });
      this.order.push(entry.clientImageId);
    }
    this.hooks.onChange();
    this.next();
  }

  retry(clientImageId: string) {
    const item = this.items.get(clientImageId);
    if (this.disposed || !item || item.status !== "failed") return;
    if (item.expired) item.requestId = crypto.randomUUID();
    item.status = "queued";
    item.error = undefined;
    item.expired = undefined;
    item.authExpired = undefined;
    this.hooks.onChange();
    this.next();
  }

  /** Retries every failed item, read from the live queue (not a render copy). */
  retryFailed() {
    for (const item of this.list()) if (item.status === "failed") this.retry(item.clientImageId);
  }

  /** After signing in again, resume the images stopped by the expired session. */
  resumeAfterLogin() {
    for (const item of this.list()) if (item.status === "failed" && item.authExpired) this.retry(item.clientImageId);
  }

  /** The slot was deleted from the text: stop and forget it. */
  cancel(clientImageId: string) {
    this.active.get(clientImageId)?.abort();
    this.active.delete(clientImageId);
    this.items.delete(clientImageId);
    this.order = this.order.filter((id) => id !== clientImageId);
    this.hooks.onChange();
    this.next();
  }

  /** Irreversible: stops running transfers and never starts queued ones. */
  dispose() {
    this.disposed = true;
    for (const controller of this.active.values()) controller.abort();
    this.active.clear();
  }

  private next() {
    if (this.disposed) return;
    for (const id of this.order) {
      if (this.active.size >= this.concurrency) return;
      const item = this.items.get(id);
      if (item?.status !== "queued") continue;
      const controller = new AbortController();
      this.active.set(id, controller);
      item.status = "uploading";
      this.hooks.onChange();
      void this.process(item, controller.signal).finally(() => {
        if (this.active.get(id) === controller) this.active.delete(id);
        this.next();
      });
    }
  }

  private finish(item: UploadItem, signal: AbortSignal, update: Partial<UploadItem>) {
    if (this.disposed || signal.aborted || this.items.get(item.clientImageId) !== item) return;
    Object.assign(item, update);
    this.hooks.onChange();
    if (update.status === "done" && update.result) this.hooks.onDone(item, update.result);
  }

  private async process(item: UploadItem, signal: AbortSignal) {
    try {
      const result = await this.upload(item, signal);
      this.finish(item, signal, { status: "done", result });
    } catch (error) {
      if (signal.aborted || this.disposed) return;
      if (error instanceof ApiError && error.status === 401) {
        this.finish(item, signal, {
          status: "failed",
          authExpired: true,
          error: "再ログインが必要です。画像は保持しており、ログイン後に送り直します。",
        });
        this.hooks.onAuthExpired?.();
        return;
      }
      const expired =
        (error instanceof ApiError && error.status === 410) ||
        (error instanceof Error && error.message === "expired");
      this.finish(item, signal, {
        status: "failed",
        expired,
        error: expired
          ? "画像の送信期限（24時間）を過ぎました。再試行すると送り直します。"
          : error instanceof Error && error.message
            ? error.message
            : SEND_FAILED,
      });
    }
  }

  private async upload(item: UploadItem, signal: AbortSignal): Promise<UploadResult> {
    const sha256 = await this.api.digest(item.blob);
    const request: UploadRequest = {
      clientImageId: item.requestId,
      fileName: item.name.slice(0, 255),
      contentType: item.type,
      byteSize: item.blob.size,
      sha256,
    };
    let resent = 0;
    for (;;) {
      if (signal.aborted) throw new Error("cancelled");
      const begun = await this.api.begin(request);
      const settled = this.settled(begun);
      if (settled) return settled;
      if (begun.status === "processing") return this.poll(begun.uploadId, signal);
      if (!begun.upload) throw new Error(SEND_FAILED);
      try {
        await this.api.send(begun.upload, item.blob, signal);
      } catch (error) {
        if (signal.aborted) throw error;
        // An expired 5-minute signature (403) or a lost transfer: sign again with
        // the same request ID, which resolves to the same upload and key.
        if (resent++ < 2) continue;
        throw new Error(SEND_FAILED);
      }
      return this.complete(begun.uploadId, signal);
    }
  }

  private settled(status: UploadStatus): UploadResult | null {
    if (status.status === "completed" && status.result) return status.result;
    if (status.status === "failed") throw new Error(status.error || SEND_FAILED);
    if (status.status === "expired") throw new Error("expired");
    return null;
  }

  private async complete(uploadId: string, signal: AbortSignal): Promise<UploadResult> {
    try {
      const done = await this.api.complete(uploadId);
      const settled = this.settled(done);
      if (settled) return settled;
    } catch (error) {
      if (signal.aborted) throw error;
      if (error instanceof ApiError) {
        const body = error.body as Partial<UploadStatus> | null;
        if (error.status === 400 && body?.status === "failed") throw new Error(error.message);
        if (error.status === 410) throw error;
        if (error.status === 401 || error.status === 403 || error.status === 404) throw error;
      }
      // Unknown or still processing: read the state instead of completing blindly.
    }
    return this.poll(uploadId, signal);
  }

  private async poll(uploadId: string, signal: AbortSignal): Promise<UploadResult> {
    let completions = 0;
    for (let attempt = 0; attempt < 40; attempt++) {
      await this.api.wait(attempt === 0 ? 500 : 1500);
      if (signal.aborted) throw new Error("cancelled");
      let status: UploadStatus;
      try {
        status = await this.api.status(uploadId);
      } catch (error) {
        if (error instanceof ApiError && error.status < 500 && error.status !== 408) throw error;
        continue;
      }
      const settled = this.settled(status);
      if (settled) return settled;
      if (status.status === "pending" && completions < 3) {
        // Not completing (released, or a stopped completion whose lease
        // expired): ask the server to complete it again.
        completions++;
        try {
          const done = await this.api.complete(uploadId);
          const again = this.settled(done);
          if (again) return again;
        } catch (error) {
          if (error instanceof ApiError && (error.status === 400 || error.status === 401 || error.status === 410)) {
            throw error.status === 400 ? new Error(error.message) : error;
          }
        }
      }
    }
    throw new Error("画像の処理結果を確認できませんでした。再試行してください。");
  }
}

async function sha256Hex(blob: Blob) {
  const digest = await crypto.subtle.digest("SHA-256", await blob.arrayBuffer());
  return [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
}

/** Sends bytes to the signed S3 POST form or the local authenticated PUT. */
async function sendBytes(target: UploadTarget, blob: Blob, signal: AbortSignal) {
  let response: Response;
  if (target.method === "PUT") {
    response = await fetch(target.url, {
      method: "PUT",
      body: blob,
      credentials: "same-origin",
      headers: { "Content-Type": "application/octet-stream" },
      signal,
    });
  } else {
    const form = new FormData();
    for (const [name, value] of Object.entries(target.fields ?? {})) form.append(name, value);
    form.append("file", blob); // S3 requires the file to be the last field
    response = await fetch(target.url, { method: "POST", body: form, mode: "cors", credentials: "omit", signal });
  }
  if (!response.ok) throw new TransferError(response.status, SEND_FAILED);
}

export function browserUploadApi(api: {
  beginUpload(request: UploadRequest): Promise<UploadStatus>;
  completeUpload(id: string): Promise<UploadStatus>;
  uploadStatus(id: string): Promise<UploadStatus>;
}): UploadApi {
  return {
    begin: (request) => api.beginUpload(request),
    complete: (id) => api.completeUpload(id),
    status: (id) => api.uploadStatus(id),
    send: sendBytes,
    digest: sha256Hex,
    wait: (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
  };
}
