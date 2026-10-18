import { createRoot } from "react-dom/client";
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { AdminPage } from "../src/components/AdminPage";
import { ArticleList } from "../src/components/ArticleList";
import { Article } from "../src/components/Article";
import { ArticleCard } from "../src/components/ArticleCard";
import "../src/styles.css";
import type { AdminPost } from "../src/lib/types";
import type { FixtureConfig } from "./fixtureTypes";

const cfg: FixtureConfig = window.fixture || {
  screen: "article",
  scenario: "success",
};
const id = "a".repeat(43);
const markdown = `## 読むことと、書くこと\n\n${"技術のこと、日々のこと。文章の流れと余白を確かめるための検証用テキストです。".repeat(8)}\n\n### コードと表\n\n\`\`\`ts\nconst example = '${"long-value-".repeat(24)}'\n\`\`\`\n\n| 項目 | 説明 |\n| --- | --- |\n| 長い列 | ${"Column".repeat(30)} |\n\n## **同じ見出し**\n\n本文内の[リンク](#article-section-1)です。\n\n## 同じ見出し\n\n![検証用の図](https://ui-fixture.invalid/diagram.svg)\n\n\`\`\`md\n## コード内は目次に含めない\n\`\`\`\n\n<script>alert('unsafe')</script>`;
const post = {
  id,
  title: "保存した変更を、いつ読者に届けるか——下書きと公開版を分けて考える",
  markdown,
  publishedAt: "2026-10-18T00:00:00Z",
  updatedAt: "2026-10-18T00:00:00Z",
};
let draft: AdminPost | null = null;
let publishedText: { title: string; markdown: string } | null = null;
let saved = false;
let signedIn = cfg.scenario !== "signed-out";
window.calls = [];
window.fetch = async (input, init) => {
  const path = String(input),
    method = init?.method || "GET";
  window.calls.push(`${method} ${path}`);
  const response = (data: unknown, status = 200) =>
    new Response(JSON.stringify(data), {
      status,
      headers: { "Content-Type": "application/json" },
    });
  if (path === "/api/session") {
    if (method === "POST") signedIn = true;
    if (method === "DELETE") signedIn = false;
    if (method === "GET" && !signedIn) return response({ error: "login required" }, 401);
    return response({ authenticated: signedIn });
  }
  if (path === "/api/admin/posts" && method === "GET") {
    if (saved && cfg.scenario === "list-failure")
      return response({ error: "一覧の取得失敗" }, 500);
    return response(draft ? [draft] : []);
  }
  if (path.startsWith("/api/admin/posts/") && method === "GET") {
    return draft ? response(draft) : response({ error: "not found" }, 404);
  }
  if (path.endsWith("/publish")) {
    if (!draft) throw new Error("Cannot publish missing fixture article");
    if (cfg.scenario === "conflict") return response({ error: "競合" }, 409);
    if (cfg.scenario === "publish-failure")
      return response({ error: "公開に失敗" }, 500);
    publishedText = { title: draft.title, markdown: draft.markdown };
    draft = {
      ...draft,
      publishedAt: draft.publishedAt ?? post.publishedAt,
      published: true,
      hasUnpublishedChanges: false,
      version: draft.version + 1,
    };
    return response(draft);
  }
  if (path.endsWith("/unpublish")) {
    if (!draft) throw new Error("Cannot hide missing fixture article");
    draft = { ...draft, published: false, version: draft.version + 1 };
    return response(draft);
  }
  if (method === "POST" || method === "PUT") {
    if (method === "PUT" && cfg.scenario === "conflict") return response({ error: "競合" }, 409);
    const input = JSON.parse(String(init?.body)) as { title: string; markdown: string };
    draft = {
      ...post,
      ...input,
      id,
      published: draft?.published || false,
      publishedAt: draft?.publishedAt ?? null,
      hasUnpublishedChanges: !publishedText || input.title !== publishedText.title || input.markdown !== publishedText.markdown,
      version: (draft?.version || 0) + 1,
    };
    saved = true;
    if (cfg.scenario === "lost-response")
      throw new TypeError("Simulated lost response");
    return response(draft);
  }
  throw new Error(`Unexpected fixture request: ${method} ${path}`);
};
function Screen() {
  return (
    <>
      <header className="site-header">
        <a className="brand" href="#">
          course_kのブログ
        </a>
        <nav>
          <a href="#">記事</a>
          <a href="#">管理</a>
        </nav>
      </header>
      <main id="main">
        {cfg.screen === "admin" ? (
          <AdminPage />
        ) : cfg.screen === "list" ? (
          <>
            <ArticleList
              posts={
                cfg.empty
                  ? []
                  : [
                      post,
                      {
                        ...post,
                        id: "b".repeat(43),
                        title: "検証用の記事：日々の記録を残す",
                      },
                    ]
              }
              page={1}
            />
            <section
              aria-label="外部記事部品の検証"
              style={{ maxWidth: 680, marginInline: "auto" }}
            >
              <ArticleCard
                article={{
                  kind: "external",
                  id: "q",
                  title: "外部記事の表示検証",
                  source: "qiita",
                  url: "https://example.com/article",
                  publishedAt: post.publishedAt,
                }}
              />
            </section>
          </>
        ) : (
          <Article post={post} />
        )}
      </main>
      <footer>
        course_k<span>検証専用の架空データ</span>
      </footer>
    </>
  );
}
const route = createRootRoute({ component: Screen });
const router = createRouter({
  routeTree: route,
  history: createMemoryHistory({ initialEntries: ["/"] }),
});
window.navigateAway = () =>
  router.navigate({ to: "/", search: { page: 2 } });
createRoot(document.getElementById("root")!).render(
  <RouterProvider router={router} />,
);
