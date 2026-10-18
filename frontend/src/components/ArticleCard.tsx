import { Link } from "@tanstack/react-router";
import { dateLabel } from "../lib/types";
import type { PublicPostSummary } from "../lib/types";

// Presentation input only. External-feed API integration is intentionally separate.
export type ArticleCardData =
  | ({ kind: "own" } & PublicPostSummary)
  | {
      kind: "external";
      id: string;
      title: string;
      publishedAt: string;
      source: "qiita" | "note" | "wantedly";
      url: string;
    };
const sourceNames = { qiita: "Qiita", note: "note", wantedly: "Wantedly" };
export function ArticleCard({ article }: { article: ArticleCardData }) {
  const external = article.kind === "external";
  const source = external ? article.source : "own";
  const label = external ? sourceNames[article.source] : "このブログ";
  const isHttpURL = external && /^https?:\/\//i.test(article.url);
  return (
    <article className={`post-row source-${source}`}>
      <div className="post-meta">
        <span className="source">
          <span className="source-dot" aria-hidden="true" />
          {label}
          {external && <span aria-hidden="true">↗</span>}
        </span>
        <time dateTime={article.publishedAt}>
          {dateLabel(article.publishedAt)}
        </time>
      </div>
      <h3>
        {external ? (
          isHttpURL ? (
            <a
              href={article.url}
              target="_blank"
              rel="noopener noreferrer"
              aria-label={`${article.title} — ${label}、新しいタブで開く`}
            >
              {article.title}
            </a>
          ) : (
            article.title
          )
        ) : (
          <Link to="/posts/$id" params={{ id: article.id }}>
            {article.title}
          </Link>
        )}
      </h3>
    </article>
  );
}
