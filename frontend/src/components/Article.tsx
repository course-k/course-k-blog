import { Link } from "@tanstack/react-router";
import { dateLabel } from "../lib/types";
import type { PublicPost } from "../lib/types";
import { Markdown } from "./Markdown";

export function Article({ post }: { post: PublicPost }) {
  return (
    <article className="reading">
      <Link to="/" search={{ page: 1 }} className="back">
        ← 記事一覧
      </Link>
      <header>
        <p className="post-meta">
          <time dateTime={post.publishedAt}>{dateLabel(post.publishedAt)}</time>
        </p>
        <h1>{post.title}</h1>
        {post.updatedAt !== post.publishedAt && (
          <p className="muted">更新：{dateLabel(post.updatedAt)}</p>
        )}
      </header>
      <Markdown text={post.markdown} toc prefix="article" />
    </article>
  );
}
