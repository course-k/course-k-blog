import { Article } from "../components/Article";
import { createFileRoute } from "@tanstack/react-router";
import { getPost } from "../lib/posts";
import { sharingMeta, siteName, siteOrigin } from "../lib/branding";

export const Route = createFileRoute("/posts/$id")({
  loader: ({ params }) => getPost({ data: params.id }),
  head: ({ loaderData }) => {
    if (!loaderData) return { meta: [{ title: `記事 — ${siteName}` }] };
    const url = `${siteOrigin}/posts/${loaderData.id}`;
    const image = new URL(`/og/posts/${loaderData.id}.png`, siteOrigin);
    image.searchParams.set("v", loaderData.updatedAt);
    const description = `${loaderData.title} — ${siteName}`;
    return {
      meta: [
        { title: description },
        { name: "description", content: description },
        ...sharingMeta({ title: loaderData.title, description, image: image.href, url, type: "article" }),
        { property: "article:published_time", content: loaderData.publishedAt },
        { property: "article:modified_time", content: loaderData.updatedAt },
      ],
    };
  },
  component: Post,
});
function Post() {
  const post = Route.useLoaderData();
  return <Article post={post} />;
}
