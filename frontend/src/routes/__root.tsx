import {
  createRootRoute,
  HeadContent,
  Link,
  Outlet,
  Scripts,
} from "@tanstack/react-router";
import "../styles.css";
import { sharingMeta, siteDescription, siteName } from "../lib/branding";

export const Route = createRootRoute({
  head: () => ({
    meta: [
      { charSet: "utf-8" },
      { name: "viewport", content: "width=device-width, initial-scale=1" },
      { title: siteName },
      {
        name: "description",
        content: siteDescription,
      },
      ...sharingMeta(),
    ],
    links: [
      { rel: "icon", href: "/favicon.ico", sizes: "16x16 32x32 48x48" },
      { rel: "icon", href: "/favicon.svg", type: "image/svg+xml", sizes: "any" },
      { rel: "apple-touch-icon", href: "/apple-touch-icon.png", sizes: "180x180" },
    ],
  }),
  component: Root,
  notFoundComponent: () => (
    <section className="notice">
      <h1>記事が見つかりません</h1>
      <p>URLをご確認ください。</p>
      <Link to="/" search={{ page: 1 }}>
        記事一覧へ
      </Link>
    </section>
  ),
  errorComponent: () => (
    <section className="notice" role="alert">
      <h1>読み込めませんでした</h1>
      <p>時間をおいて再度お試しください。</p>
      <a href="/">記事一覧へ</a>
    </section>
  ),
});
function Root() {
  return (
    <html lang="ja">
      <head>
        <HeadContent />
      </head>
      <body>
        <a className="skip" href="#main">
          本文へ移動
        </a>
        <header className="site-header">
          <Link to="/" search={{ page: 1 }} className="brand" aria-label="course_kのブログのトップへ">
            <img src="/brand/mark.svg" width="35" height="28" alt="" />
          </Link>
          <nav aria-label="メイン">
            <Link to="/" search={{ page: 1 }}>
              記事
            </Link>
            <Link to="/admin">管理</Link>
          </nav>
        </header>
        <main id="main">
          <Outlet />
        </main>
        <footer>
          course_k <span>技術のこと、日々のこと。</span>
        </footer>
        <Scripts />
      </body>
    </html>
  );
}
