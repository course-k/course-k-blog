import { ArticleList } from "../components/ArticleList";
import { createFileRoute, stripSearchParams } from "@tanstack/react-router";
import { listPosts } from "../lib/posts";

export const Route = createFileRoute("/")({
  validateSearch: (
    search: Record<string, unknown>,
  ): { page: number; month?: string } => ({
    month:
      typeof search.month === "string" &&
      /^[1-9][0-9]{3}-(0[1-9]|1[0-2])$/.test(search.month)
        ? search.month
        : undefined,
    page: Math.min(
      10000,
      Math.max(
        1,
        Number.isInteger(Number(search.page)) ? Number(search.page) : 1,
      ),
    ),
  }),
  search: {
    middlewares: [stripSearchParams({ page: 1 })],
  },
  loaderDeps: ({ search: { page, month } }) => ({ page, month }),
  loader: ({ deps }) => listPosts({ data: deps }),
  component: Home,
});
function Home() {
  const result = Route.useLoaderData();
  const { page, month } = Route.useSearch();
  return (
    <ArticleList
      posts={result.articles}
      hasNext={result.hasNext}
      page={page}
      month={month}
      months={result.months}
    />
  );
}
