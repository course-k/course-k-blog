import { Link } from "@tanstack/react-router";
import { ArticleCard } from "./ArticleCard";
import { AuthorProfile } from "./AuthorProfile";
import { MonthArchive, monthLabel } from "./MonthArchive";
import type { ArticleSummary, ArchiveMonth } from "../lib/types";

export function ArticleList({
  posts,
  page,
  hasNext = false,
  month,
  months = [],
}: {
  posts: ArticleSummary[];
  page: number;
  hasNext?: boolean;
  month?: string;
  months?: ArchiveMonth[];
}) {
  return (
    <>
      <AuthorProfile />
      <MonthArchive months={months} selected={month} />
      <section aria-label="記事一覧">
        <div className="section-label">
          <h2>{month ? `${monthLabel(month)}の記事` : "記事"}</h2>
          <span>新しい順</span>
        </div>
        {month && (
          <Link className="archive-reset" to="/" search={{ page: 1 }}>
            すべての記事に戻る
          </Link>
        )}
        {posts.length === 0 ? (
          <div className="empty">
            <h3>
              {page > 1
                ? "このページの記事はありません"
                : month
                  ? "この月の記事はありません"
                  : "最初の記事を準備しています"}
            </h3>
            <p>公開された記事が、ここに並びます。</p>
          </div>
        ) : (
          posts.map((post) => (
            <ArticleCard key={`${post.kind}:${post.id}`} article={post} />
          ))
        )}
        <nav className="pagination" aria-label="ページ">
          {page > 1 && (
            <Link to="/" search={{ page: page - 1, month }}>
              ← 前のページ
            </Link>
          )}
          {hasNext && (
            <Link to="/" search={{ page: page + 1, month }}>
              次のページ →
            </Link>
          )}
        </nav>
      </section>
    </>
  );
}
