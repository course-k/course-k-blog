import { createServerFn } from "@tanstack/react-start";
import { notFound } from "@tanstack/react-router";
import type { PublicPost, ArticlePage } from "./types";

export const listPosts = createServerFn({ method: "GET" })
  .validator((page: number) => {
    if (!Number.isInteger(page) || page < 1 || page > 10000)
      throw new Error("invalid page");
    return page;
  })
  .handler(async ({ data }): Promise<ArticlePage> => {
    const base = process.env.API_BASE_URL || "http://127.0.0.1:8081";
    const response = await fetch(`${base}/api/articles?page=${data}`, {
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
