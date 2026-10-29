import { createServerFn } from "@tanstack/react-start";
import { notFound } from "@tanstack/react-router";
import type { PublicPost, ArticlePage } from "./types";

export const listPosts = createServerFn({ method: "GET" })
  .validator(({ page, month }: { page: number; month?: string }) => {
    if (
      !Number.isInteger(page) || page < 1 || page > 10000 ||
      (month !== undefined && !/^[1-9][0-9]{3}-(0[1-9]|1[0-2])$/.test(month))
    )
      throw new Error("invalid article filter");
    return { page, month };
  })
  .handler(async ({ data }): Promise<ArticlePage> => {
    const base = process.env.API_BASE_URL || "http://127.0.0.1:8081";
    const query = new URLSearchParams({ page: String(data.page) });
    if (data.month) query.set("month", data.month);
    const response = await fetch(`${base}/api/articles?${query}`, {
      signal: AbortSignal.timeout(5000),
    });
    if (!response.ok) throw new Error("記事を取得できませんでした。");
    return response.json();
  });

export const getPost = createServerFn({ method: "GET" })
  .validator((id: string) => {
    if (!/^[A-Za-z0-9_-]{43}$/.test(id)) throw notFound();
    return id;
  })
  .handler(async ({ data }): Promise<PublicPost> => {
    const base = process.env.API_BASE_URL || "http://127.0.0.1:8081";
    const response = await fetch(
      `${base}/api/posts/${encodeURIComponent(data)}`,
      { signal: AbortSignal.timeout(5000) },
    );
    if (response.status === 404) throw notFound();
    if (!response.ok) throw new Error("記事を取得できませんでした。");
    return response.json();
  });
