import { Link } from "@tanstack/react-router";
import { ArticleCard } from "./ArticleCard";
import type { PublicPostSummary } from "../lib/types";

export function ArticleList({
  posts,
  page,
}: {
  posts: PublicPostSummary[];
  page: number;
}) {
  return (
    <>
      <section className="intro">
        <h1>書いたもの</h1>
        <p>技術のこと、日々のこと。書いたものをまとめています。</p>
      </section>
      <section aria-label="記事一覧">
        <div className="section-label">
          <h2>記事</h2>
          <span>新しい順</span>
        </div>
        {posts.length === 0 ? (
          <div className="empty">
            <h3>
              {page === 1
                ? "最初の記事を準備しています"
                : "このページの記事はありません"}
            </h3>
            <p>公開された記事が、ここに並びます。</p>
          </div>
        ) : (
          posts.map((post) => (
            <ArticleCard key={post.id} article={{ kind: "own", ...post }} />
          ))
        )}
        <nav className="pagination" aria-label="ページ">
          {page > 1 && (
            <Link to="/" search={{ page: page - 1 }}>
              ← 前のページ
            </Link>
          )}
          {posts.length === 20 && (
            <Link to="/" search={{ page: page + 1 }}>
              次のページ →
            </Link>
          )}
        </nav>
      </section>
    </>
  );
}
