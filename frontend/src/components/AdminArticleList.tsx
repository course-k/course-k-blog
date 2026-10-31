import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import type { AdminPost } from "../lib/types";
import { adminApi, ApiError } from "../features/editor/adminApi";

const dateTime = new Intl.DateTimeFormat("ja-JP", {
  timeZone: "Asia/Tokyo",
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
});

export function stateLabel(post: AdminPost) {
  if (!post.published) return "下書き";
  return post.hasUnpublishedChanges ? "公開中・未公開の変更あり" : "公開中";
}

/** Own articles only, newest update first; ties keep a stable ID order. */
export function sortForList(posts: AdminPost[]) {
  return [...posts].sort(
    (a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0),
  );
}

export function AdminArticleList({ onAuthExpired }: { onAuthExpired(): void }) {
  const [posts, setPosts] = useState<AdminPost[] | null>(null);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let active = true;
    adminApi
      .list()
      .then((list) => {
        if (active) {
          setPosts(sortForList(list));
          setError("");
        }
      })
      .catch((cause: unknown) => {
        if (!active) return;
        if (cause instanceof ApiError && cause.status === 401) onAuthExpired();
        setError("記事一覧を取得できませんでした。");
      });
    return () => {
      active = false;
    };
  }, [attempt, onAuthExpired]);
  return (
    <section className="admin-articles" aria-labelledby="articles-heading">
      <h1 id="articles-heading">記事一覧</h1>
      {error && (
        <p className="error" role="alert">
          {error}{" "}
          <button type="button" className="link-button" onClick={() => setAttempt((n) => n + 1)}>
            再読み込み
          </button>
        </p>
      )}
      {posts === null && !error && <p role="status">読み込んでいます…</p>}
      {posts?.length === 0 && <p className="muted">記事はありません</p>}
      {posts && posts.length > 0 && (
        <table className="article-table">
          <thead>
            <tr>
              <th scope="col">タイトル</th>
              <th scope="col">状態</th>
              <th scope="col">更新日時</th>
            </tr>
          </thead>
          <tbody>
            {posts.map((post) => (
              <tr key={post.id}>
                <td>
                  {/* The editor reads the latest article; this row's copy is not used. */}
                  <Link to="/admin" search={{ view: "edit", id: post.id }} className={post.title ? undefined : "untitled"}>
                    {post.title || "無題"}
                  </Link>
                </td>
                <td>{stateLabel(post)}</td>
                <td>
                  <time dateTime={post.updatedAt}>{dateTime.format(new Date(post.updatedAt))}</time>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
