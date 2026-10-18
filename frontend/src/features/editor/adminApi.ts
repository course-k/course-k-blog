import type { Draft } from "../../lib/types";

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}
async function api<T>(
  path: string,
  method = "GET",
  body?: unknown,
): Promise<T> {
  const response = await fetch(`/api${path}`, {
    method,
    credentials: "same-origin",
    cache: "no-store",
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await response.json();
  if (!response.ok)
    throw new ApiError(response.status, data.error || "処理できませんでした。");
  return data;
}

// Transport contracts stay here; workflow decisions belong to useAdminEditor.
export const adminApi = {
  session: () => api<void>("/session"),
  login: (password: string) => api<void>("/session", "POST", { password }),
  logout: () => api<void>("/session", "DELETE", {}),
  list: () => api<Draft[]>("/admin/posts"),
  create: (title: string, markdown: string) =>
    api<Draft>("/admin/posts", "POST", { title, markdown }),
  save: (id: string, title: string, markdown: string, version: number) =>
    api<Draft>(`/admin/posts/${id}`, "PUT", { title, markdown, version }),
  publish: (id: string, version: number) =>
    api<Draft>(`/admin/posts/${id}/publish`, "POST", { version }),
  unpublish: (id: string, version: number) =>
    api<Draft>(`/admin/posts/${id}/unpublish`, "POST", { version }),
};
