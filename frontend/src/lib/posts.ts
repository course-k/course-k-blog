import { createServerFn } from "@tanstack/react-start";
import { notFound } from "@tanstack/react-router";
import type { PublicPost, ArticlePage } from "./types";
import { loadArticlePage, loadPost, NotFound } from "../../publicData.mjs";

export const listPosts = createServerFn({ method: "GET" })
  .validator(({ page, month }: { page: number; month?: string }) => {
    if (
      !Number.isInteger(page) || page < 1 || page > 10000 ||
      (month !== undefined && !/^[1-9][0-9]{3}-(0[1-9]|1[0-2])$/.test(month))
    )
      throw new Error("invalid article filter");
    return { page, month };
  })
  .handler(({ data }): Promise<ArticlePage> => loadArticlePage(data));

export const getPost = createServerFn({ method: "GET" })
  .validator((id: string) => {
    if (!/^[A-Za-z0-9_-]{43}$/.test(id)) throw notFound();
    return id;
  })
  .handler(async ({ data }): Promise<PublicPost> => {
    try {
      return await loadPost(data);
    } catch (error) {
      if (error instanceof NotFound) throw notFound();
      throw error;
    }
  });
