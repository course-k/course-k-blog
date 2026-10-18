import type { AdminPost } from "../../lib/types";

const REQUEST_TIMEOUT_MS = 15_000;

export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export class ResponseFormatError extends Error {}

// A rejected write (4xx) differs from a request whose commit cannot be confirmed.
export function outcomeIsUnknown(error: unknown): boolean {
  return (
    !(error instanceof ApiError) || error.status >= 500 || error.status === 408
  );
}

export async function requestAdminJson<T>(
  path: string,
  method = "GET",
  body?: unknown,
  signal: AbortSignal = AbortSignal.timeout(REQUEST_TIMEOUT_MS),
): Promise<T> {
  const response = await fetch(`/api${path}`, {
    method,
    credentials: "same-origin",
    cache: "no-store",
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  });
  let data: unknown;
  try {
    data = await response.json();
  } catch {
    if (!response.ok)
      throw new ApiError(response.status, "処理できませんでした。");
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
    throw new ApiError(response.status, message);
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
  create: (title: string, markdown: string, creationKey: string) =>
    requestAdminJson<AdminPost>("/admin/posts", "POST", {
      title,
      markdown,
      creationKey,
    }),
  save: (id: string, title: string, markdown: string, version: number) =>
    requestAdminJson<AdminPost>(`/admin/posts/${id}`, "PUT", {
      title,
      markdown,
      version,
    }),
  publish: (id: string, version: number) =>
    requestAdminJson<AdminPost>(`/admin/posts/${id}/publish`, "POST", {
      version,
    }),
  unpublish: (id: string, version: number) =>
    requestAdminJson<AdminPost>(`/admin/posts/${id}/unpublish`, "POST", {
      version,
    }),
};
