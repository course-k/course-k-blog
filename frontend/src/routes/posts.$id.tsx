import { Article } from "../components/Article";
import { createFileRoute } from "@tanstack/react-router";
import { getPost } from "../lib/posts";

export const Route = createFileRoute("/posts/$id")({
  loader: ({ params }) => getPost({ data: params.id }),
  head: ({ loaderData }) => ({
    meta: [
      {
        title: loaderData
          ? `${loaderData.title} — course_kのブログ`
          : "記事 — course_kのブログ",
      },
    ],
  }),
  component: Post,
});
function Post() {
  const post = Route.useLoaderData();
  return <Article post={post} />;
}
