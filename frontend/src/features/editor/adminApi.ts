import type { AdminPost, SyncState } from "../../lib/types";

const REQUEST_TIMEOUT_MS = 15_000;
// Completion verifies and re-encodes up to 10MB on the server (20 s budget).
const COMPLETE_TIMEOUT_MS = 30_000;
// Rewriting the reader files may take up to 25 s on the server.
const PUBLICATION_TIMEOUT_MS = 30_000;

export type UploadTarget = {
  method: "POST" | "PUT";
  url: string;
  fields?: Record<string, string>;
  expiresAt: string;
};
export type UploadResult = { id: string; url: string; width: number; height: number };
export type UploadStatus = {
  uploadId: string;
  status: "pending" | "processing" | "completed" | "failed" | "expired";
  upload?: UploadTarget;
  result?: UploadResult;
  failureCode?: string;
  error?: string;
};
export type UploadRequest = {
  clientImageId: string;
  fileName: string;
  contentType: string;
  byteSize: number;
  sha256: string;
};

export class ApiError extends Error {
  readonly status: number;
  /** Seconds from Retry-After, when the server sent one. */
  readonly retryAfter: number | null;
  readonly body: unknown;

  constructor(status: number, message: string, retryAfter: number | null = null, body: unknown = null) {
    super(message);
    this.status = status;
    this.retryAfter = retryAfter;
    this.body = body;
  }
}

export class ResponseFormatError extends Error {}

// A rejected write (4xx) differs from a request whose commit cannot be confirmed.
export function outcomeIsUnknown(error: unknown): boolean {
  return (
    !(error instanceof ApiError) || error.status >= 500 || error.status === 408
  );
}

async function sha256Hex(text: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text));
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

export async function requestAdminJson<T>(
  path: string,
  method = "GET",
  body?: unknown,
  signal: AbortSignal = AbortSignal.timeout(REQUEST_TIMEOUT_MS),
): Promise<T> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  const payload = body === undefined ? undefined : JSON.stringify(body);
  // The signed origin in front of the API accepts a request body only together
  // with its SHA-256. Elsewhere the header is ignored.
  if (payload !== undefined) headers["x-amz-content-sha256"] = await sha256Hex(payload);
  const response = await fetch(`/api${path}`, {
    method,
    credentials: "same-origin",
    cache: "no-store",
    headers,
    body: payload,
    signal,
  });
  const retryHeader = Number(response.headers.get("Retry-After"));
  const retryAfter = Number.isFinite(retryHeader) && retryHeader > 0 ? retryHeader : null;
  let data: unknown;
  try {
    data = await response.json();
  } catch {
    if (!response.ok)
      throw new ApiError(response.status, "処理できませんでした。", retryAfter);
    throw new ResponseFormatError("応答の形式を確認できませんでした。");
  }
  if (!response.ok) {
    const message =
      data &&
      typeof data === "object" &&
      "error" in data &&
      typeof data.error === "string"
        ? data.error
        : "処理できませんでした。";
    throw new ApiError(response.status, message, retryAfter, data);
  }
  return data as T;
}

// Transport contracts stay here; workflow decisions belong to useAdminEditor.
export const adminApi = {
  session: (signal?: AbortSignal) =>
    requestAdminJson<void>("/session", "GET", undefined, signal),
  login: (password: string) =>
    requestAdminJson<void>("/session", "POST", { password }),
  logout: () => requestAdminJson<void>("/session", "DELETE", {}),
  list: () => requestAdminJson<AdminPost[]>("/admin/posts"),
  findCreation: (key: string) =>
    requestAdminJson<AdminPost>(
      `/admin/creation-results/${encodeURIComponent(key)}`,
    ),
  get: (id: string) =>
    requestAdminJson<AdminPost>(`/admin/posts/${encodeURIComponent(id)}`),
  create: (doc: { title: string; markdown: string }, creationKey: string) =>
    requestAdminJson<AdminPost>("/admin/posts", "POST", {
      title: doc.title,
      markdown: doc.markdown,
      creationKey,
    }),
  save: (id: string, doc: { title: string; markdown: string }, version: number) =>
    requestAdminJson<AdminPost>(`/admin/posts/${encodeURIComponent(id)}`, "PUT", {
      title: doc.title,
      markdown: doc.markdown,
      version,
    }),
  publish: (id: string, version: number) =>
    requestAdminJson<AdminPost>(`/admin/posts/${encodeURIComponent(id)}/publish`, "POST", {
      version,
    }),
  unpublish: (id: string, version: number) =>
    requestAdminJson<AdminPost>(`/admin/posts/${encodeURIComponent(id)}/unpublish`, "POST", {
      version,
    }),
  // Whether the files readers are served from match the database. The API
  // answers 404 when it does not publish files at all.
  publicationState: () => requestAdminJson<{ current: boolean }>("/admin/publication"),
  refreshPublication: () =>
    requestAdminJson<{ current: boolean }>(
      "/admin/publication",
      "POST",
      {},
      AbortSignal.timeout(PUBLICATION_TIMEOUT_MS),
    ),
  beginUpload: (request: UploadRequest) =>
    requestAdminJson<UploadStatus>("/admin/image-uploads", "POST", request),
  completeUpload: (uploadId: string) =>
    requestAdminJson<UploadStatus>(
      `/admin/image-uploads/${encodeURIComponent(uploadId)}/complete`,
      "POST",
      {},
      AbortSignal.timeout(COMPLETE_TIMEOUT_MS),
    ),
  uploadStatus: (uploadId: string) =>
    requestAdminJson<UploadStatus>(`/admin/image-uploads/${encodeURIComponent(uploadId)}`),
  syncStates: () => requestAdminJson<SyncState[]>("/admin/sync"),
  requestSync: (source: SyncState["source"]) =>
    requestAdminJson<{ queued: boolean }>(`/admin/sync/${source}`, "POST", {}),
};
