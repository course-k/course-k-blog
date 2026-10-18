import { ArticleList } from "../components/ArticleList";
import { createFileRoute } from "@tanstack/react-router";
import { listPosts } from "../lib/posts";

export const Route = createFileRoute("/")({
  validateSearch: (search: Record<string, unknown>) => ({
    page: Math.min(
      10000,
      Math.max(
        1,
        Number.isInteger(Number(search.page)) ? Number(search.page) : 1,
      ),
    ),
  }),
  loaderDeps: ({ search: { page } }) => ({ page }),
  loader: ({ deps }) => listPosts({ data: deps.page }),
  component: Home,
});
function Home() {
  const posts = Route.useLoaderData();
  const { page } = Route.useSearch();
  return <ArticleList posts={posts} page={page} />;
}
