import test from "node:test";
import assert from "node:assert/strict";
import ssrEntry from "../dist/server/index.js";
import { createHandler } from "../runtime.mjs";
const entry = {
  fetch: createHandler({
    render: request => ssrEntry.fetch(request),
    staticFiles: (_, next) => next(),
  }),
};
const id = "a".repeat(43);
const post = {
  id,
  title: "SSR検証の記事",
  markdown:
    "## **同じ見出し**\n\n文章です。\n\n## 同じ見出し\n\n```md\n## コード内\n```\n\n<script>unsafe()</script>",
  publishedAt: "2026-10-18T00:00:00Z",
  updatedAt: "2026-10-18T00:00:00Z",
};
// No network or real API. Only production SSR and a deterministic data fixture.
let mode = "success";
let lastArticleURL;
globalThis.fetch = async (input) => {
  const url = String(input);
  if (!/^http:\/\/127\.0\.0\.1:8081\/api\/(posts|articles)/.test(url))
    throw new Error(`Unexpected network attempt: ${url}`);
  if (url.includes("/api/articles?")) lastArticleURL = url;
  const data =
    mode === "failure"
      ? { error: "fixture failure" }
      : mode === "missing"
        ? {}
        : url.includes("?")
          ? {
              months: [{ month: "2026-10", count: 22 }, { month: "2025-12", count: 1 }],
              articles: [
                { kind: "own", ...post, markdown: undefined },
                {
                  kind: "external",
                  id: "qiita:fixture",
                  title: "外部の技術記事",
                  url: "https://qiita.com/course_k/items/fixture",
                  source: "qiita",
                  publishedAt: post.publishedAt,
                  updatedAt: post.updatedAt,
                },
              ],
              hasNext: mode === "next",
            }
          : post;
  return new Response(JSON.stringify(data), {
    status: mode === "failure" ? 500 : mode === "missing" ? 404 : 200,
  });
};
test("production SSR contains article, unique TOC anchors and safe Markdown without JS", async () => {
  mode = "success";
  const response = await entry.fetch(
    new Request(`http://ui-fixture.invalid/posts/${id}`),
  );
  const html = await response.text();
  assert.equal(response.status, 200);
  assert(html.includes("SSR検証の記事"));
  assert(html.includes('id="article-section-1"'));
  assert(html.includes('id="article-section-2"'));
  assert(!html.includes('id="article-section-3"'));
  assert(html.includes('href="#article-section-2"'));
  assert(!html.includes("<script>unsafe()</script>"));
  assert(html.includes("course_kのブログ"));
  assert.match(html, /class="back" href="\/"/);
});
test("production SSR list links to published article and has no excerpt", async () => {
  mode = "success";
  const response = await entry.fetch(
    new Request("http://ui-fixture.invalid/"),
  );
  const html = await response.text();
  assert.equal(response.status, 200);
  assert(html.includes(`/posts/${id}`));
  assert(html.includes("post-row source-own"));
  assert(!html.includes("文章です。"));
  assert(html.includes('href="https://qiita.com/course_k/items/fixture"'));
  assert(html.includes("外部の技術記事"));
  assert(!html.includes("次のページ"));
});
test("missing and failed article are not rendered as empty article list", async () => {
  mode = "missing";
  const missing = await entry.fetch(
    new Request(`http://ui-fixture.invalid/posts/${id}`),
  );
  assert.equal(missing.status, 404);
  assert((await missing.text()).includes("記事が見つかりません"));
  mode = "failure";
  const failed = await entry.fetch(
    new Request("http://ui-fixture.invalid/"),
  );
  assert.equal(failed.status, 500);
  const html = await failed.text();
  assert(!html.includes("最初の記事を準備しています"));
});

test("SSR uses explicit next-page contract rather than the current item count", async () => {
  mode = "next";
  const response = await entry.fetch(
    new Request("http://ui-fixture.invalid/"),
  );
  assert.equal(response.status, 200);
  assert((await response.text()).includes("次のページ"));
});

test("SSR keeps the homepage at / and generates clean home links", async () => {
  mode = "next";
  const response = await entry.fetch(new Request("http://ui-fixture.invalid/"));
  assert.equal(response.status, 200);
  assert.equal(response.headers.get("location"), null);
  const html = await response.text();
  assert.match(html, /<a(?=[^>]*class="brand[^"<>]*")(?=[^>]*href="\/")[^>]*>/);
  assert(html.includes('href="/?page=2"'));
  assert(!html.includes('href="/?page=1"'));
  assert(!html.includes("前のページ"));
});

test("production handler permanently redirects the explicit first page to /", async () => {
  mode = "success";
  const response = await entry.fetch(
    new Request("http://ui-fixture.invalid/?page=1"),
  );
  assert.equal(response.status, 308);
  assert.equal(response.headers.get("location"), "/");
});

test("SSR keeps later page numbers and returns from page 2 to /", async () => {
  mode = "next";
  for (const page of [2, 3]) {
    const response = await entry.fetch(
      new Request(`http://ui-fixture.invalid/?page=${page}`),
    );
    assert.equal(response.status, 200);
    assert.equal(response.headers.get("location"), null);
    const html = await response.text();
    const previous = page === 2 ? "/" : `/?page=${page - 1}`;
    const previousLink = html.match(/<a\b[^>]*>← 前のページ<\/a>/)?.[0];
    assert(previousLink?.includes(`href="${previous}"`));
    const nextLink = html.match(/<a\b[^>]*>次のページ →<\/a>/)?.[0];
    assert(nextLink?.includes(`href="/?page=${page + 1}"`));
    assert(!html.includes('href="/?page=1"'));
  }
});


test("SSR month filter is shareable, retains pagination and resets to all articles", async () => {
  mode = "next";
  const response = await entry.fetch(new Request("http://ui-fixture.invalid/?month=2026-10&page=2"));
  const html = await response.text();
  assert.equal(response.status, 200);
  assert.equal(new URL(lastArticleURL).searchParams.get("month"), "2026-10");
  assert.equal(new URL(lastArticleURL).searchParams.get("page"), "2");
  assert(html.includes("2026年10月の記事"));
  assert.match(html, /href="\/\?month=2026-10"[^>]*>← 前のページ/);
  assert.match(html, /href="\/\?page=3&amp;month=2026-10"[^>]*>次のページ/);
  assert.match(html, /href="\/"[^>]*>すべての記事に戻る/);
  assert(html.includes('data-selected="true"'));
  assert(!html.includes('page=1'));
  const redirect = await entry.fetch(new Request("http://ui-fixture.invalid/?month=2026-10&page=1"));
  assert.equal(redirect.headers.get("location"), "/?month=2026-10");
});

test("invalid month cannot reach the article API", async () => {
  mode = "success";
  lastArticleURL = undefined;
  const response = await entry.fetch(new Request("http://ui-fixture.invalid/?month=2026-13"));
  assert.equal(response.status, 307);
  assert.equal(response.headers.get("location"), "/");
  assert.equal(lastArticleURL, undefined);
});

test("home SSR advertises real brand assets and keeps the logo out of the profile avatar", async () => {
  mode = "success";
  const response = await entry.fetch(new Request("http://ui-fixture.invalid/"));
  const html = await response.text();
  assert.match(html, /<link[^>]+rel="icon"[^>]+href="\/favicon\.svg"/);
  assert.match(html, /<link[^>]+rel="apple-touch-icon"[^>]+href="\/apple-touch-icon\.png"/);
  assert.match(html, /<meta[^>]+property="og:image"[^>]+content="https:\/\/blog\.course-k\.com\/brand\/ogp-home\.png"/);
  assert.match(html, /<meta[^>]+name="twitter:card"[^>]+content="summary_large_image"/);
  assert.match(html, /<h1[^>]*><img[^>]+src="\/brand\/wordmark\.png"[^>]+alt="course_kのブログ"/);
  assert.match(html, /<span class="author-avatar"><img[^>]+src="\/profile\/rider\.webp"[^>]+alt="バイクに乗るcourse_k"/);
  assert.match(html, /aria-label="course_kのブログのトップへ"[^>]*><img[^>]+src="\/brand\/mark\.svg"/);
});

test("article SSR overrides the home sharing card with the published article", async () => {
  mode = "success";
  const response = await entry.fetch(new Request(`http://ui-fixture.invalid/posts/${id}`));
  const html = await response.text();
  assert.equal(response.status, 200);
  assert.match(html, /<meta[^>]+property="og:title"[^>]+content="SSR検証の記事"/);
  assert.match(html, /<meta[^>]+property="og:type"[^>]+content="article"/);
  assert.match(html, new RegExp(`<meta[^>]+property="og:image"[^>]+content="https://blog\\.course-k\\.com/og/posts/${id}\\.png`));
  assert.equal((html.match(/property="og:image" /g) || []).length, 1);
  assert(!html.includes('content="https://blog.course-k.com/brand/ogp-home.png"'));
});

test("production SSR renders image settings without JavaScript and keeps settings out of title", async () => {
  mode = "success";
  const original = post.markdown;
  post.markdown = `![図](/api/images/${id} "blog-image:v1;width=40;align=center;title=%E8%AA%AC%E6%98%8E")\n\n![旧](/api/images/${id})\n\n![危険](/x.png "blog-image:v1;width=1;align=<b>")`;
  try {
    const html = await (await entry.fetch(new Request(`http://ui-fixture.invalid/posts/${id}`))).text();
    assert.match(html, /<span class="blog-image" data-align="center" style="--image-width:40%">/);
    assert.match(html, /<img src="\/api\/images\/a{43}" alt="図" title="説明"\/>/);
    assert.match(html, /<span class="blog-image" data-align="left"><span class="blog-image-frame"><button type="button" class="image-zoom" aria-label="画像を拡大表示：旧">/);
    assert(!/title="blog-image/.test(html), "reserved settings never reach the title attribute");
    assert(!html.includes("<b>"));
  } finally {
    post.markdown = original;
  }
});

test("admin entry normalizes to the new-article view", async () => {
  const response = await entry.fetch(new Request("http://ui-fixture.invalid/admin"));
  assert.equal(response.status, 307);
  assert.equal(response.headers.get("location"), "/admin?view=new");
});

for (const path of ["/admin?view=new", "/admin?view=edit&id=" + id, "/admin?view=settings"]) {
  test(`admin SSR ${path} is noindex and contains no article data before sign-in`, async () => {
    const before = lastArticleURL;
    const response = await entry.fetch(new Request(`http://ui-fixture.invalid${path}`));
    const html = await response.text();
    assert.equal(response.status, 200);
    assert.match(html, /<meta name="robots" content="noindex, nofollow"\/?>/);
    assert(html.includes("ログイン状態を確認しています"));
    assert(!html.includes("SSR検証の記事"));
    assert(!html.includes("textarea"));
    assert.equal(lastArticleURL, before);
  });
}
