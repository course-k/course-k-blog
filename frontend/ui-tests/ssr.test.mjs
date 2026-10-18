import test from "node:test";
import assert from "node:assert/strict";
import entry from "../dist/server/index.js";
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
globalThis.fetch = async (input) => {
  const url = String(input);
  if (!url.startsWith("http://127.0.0.1:8081/api/posts"))
    throw new Error(`Unexpected network attempt: ${url}`);
  const data =
    mode === "failure"
      ? { error: "fixture failure" }
      : mode === "missing"
        ? {}
        : url.includes("?")
          ? [{ ...post, markdown: undefined }]
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
});
test("production SSR list links to published article and has no excerpt", async () => {
  mode = "success";
  const response = await entry.fetch(
    new Request("http://ui-fixture.invalid/?page=1"),
  );
  const html = await response.text();
  assert.equal(response.status, 200);
  assert(html.includes(`/posts/${id}`));
  assert(html.includes("post-row source-own"));
  assert(!html.includes("文章です。"));
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
    new Request("http://ui-fixture.invalid/?page=1"),
  );
  assert.equal(failed.status, 500);
  const html = await failed.text();
  assert(!html.includes("最初の記事を準備しています"));
});
