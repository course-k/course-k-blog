import { createRoot } from "react-dom/client";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  Outlet,
  RouterProvider,
  stripSearchParams,
} from "@tanstack/react-router";
import { AdminPage } from "../src/components/AdminPage";
import { ArticleList } from "../src/components/ArticleList";
import { Article } from "../src/components/Article";
import { ArticleCard } from "../src/components/ArticleCard";
import { validateAdminSearch } from "../src/features/editor/adminSearch";
import "../src/styles.css";
import type { AdminPost, SyncState } from "../src/lib/types";
import type { FailureRule, FixtureConfig, FixtureServer } from "./fixtureTypes";

const cfg: FixtureConfig = window.fixture || { screen: "article", scenario: "success" };
const id = "a".repeat(43);
const markdown = `## 読むことと、書くこと\n\n${"技術のこと、日々のこと。文章の流れと余白を確かめるための検証用テキストです。".repeat(8)}\n\n### コードと表\n\n\`\`\`ts\nconst example = '${"long-value-".repeat(24)}'\n\`\`\`\n\n| 項目 | 説明 |\n| --- | --- |\n| 長い列 | ${"Column".repeat(30)} |\n\n## **同じ見出し**\n\n本文内の[リンク](#article-section-1)です。\n\n## 同じ見出し\n\n![検証用の図](https://ui-fixture.invalid/diagram.svg)\n\n\`\`\`md\n## コード内は目次に含めない\n\`\`\`\n\n<script>alert('unsafe')</script>`;
const post = {
  kind: "own" as const,
  id,
  title: "保存した変更を、いつ読者に届けるか——下書きと公開版を分けて考える",
  markdown: cfg.articleMarkdown ?? markdown,
  publishedAt: "2026-10-18T00:00:00Z",
  updatedAt: "2026-10-18T00:00:00Z",
};

// ---- fixed API ----------------------------------------------------------
let clock = Date.parse("2026-10-31T00:00:00Z");
const stamp = () => new Date((clock += 60_000)).toISOString();
const posts: Record<string, AdminPost> = {};
const published: Record<string, { title: string; markdown: string }> = {};
for (const [index, partial] of (cfg.posts ?? []).entries()) {
  const postId = partial.id ?? String.fromCharCode(98 + index).repeat(43);
  posts[postId] = {
    id: postId,
    title: "",
    markdown: "",
    version: 1,
    published: false,
    publishedAt: null,
    updatedAt: stamp(),
    hasUnpublishedChanges: true,
    ...partial,
  };
  if (posts[postId].published) published[postId] = { title: posts[postId].title, markdown: posts[postId].markdown };
}
const creations: Record<string, { postId: string; payload: string }> = {};
type Upload = { id: string; request: { clientImageId: string; fileName: string; contentType: string; byteSize: number }; status: string; received: boolean; result?: unknown; failure?: string };
const uploads: Record<string, Upload> = {};
const rules: FailureRule[] = [];
let held: (() => void)[] = [];
let nextId = 1;
const sync: SyncState[] = (["qiita", "wantedly", "note"] as const).map((source) => ({
  source,
  status: "idle",
  requested: false,
  coverage: "unknown",
  count: 0,
  errorCode: "",
  lastAttempt: null,
  lastSuccess: null,
  nextAllowedAt: null,
  ...(cfg.sync?.find((state) => state.source === source) ?? {}),
}));
const server: FixtureServer = {
  posts,
  calls: [],
  bodies: [],
  fail: (rule) => rules.push({ times: 1, ...rule }),
  release: () => {
    const waiting = held;
    held = [];
    for (const resolve of waiting) resolve();
  },
  signedIn: cfg.scenario !== "signed-out",
  sync,
  imagesRegistered: 0,
  uploadFailures: {},
};
window.fixtureServer = server;
window.calls = server.calls;

const json = (data: unknown, status = 200, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(data), { status, headers: { "Content-Type": "application/json", ...headers } });
const view = (p: AdminPost) => {
  const live = published[p.id];
  return { ...p, hasUnpublishedChanges: !p.published || !live || live.title !== p.title || live.markdown !== p.markdown };
};

async function handle(method: string, path: string, body: unknown): Promise<Response> {
  if (path === "/api/auth/config") return json({ mode: cfg.oidc ? "oidc" : "password" });
  if (path === "/api/session") {
    if (method === "POST") server.signedIn = true;
    if (method === "DELETE") server.signedIn = false;
    if (method === "GET" && !server.signedIn) return json({ error: "login required" }, 401);
    return json({ authenticated: server.signedIn });
  }
  if (path.startsWith("/api/admin/") && !server.signedIn) return json({ error: "login required" }, 401);
  if (path === "/api/admin/operations")
    return json({ state: { generation: 1, backedUpGeneration: 0, running: false, lastSuccess: null, lastError: "", dueAt: null }, backups: [] });
  if (path === "/api/admin/backups") return json({ queued: true }, 202);
  if (path === "/api/admin/sync") return json(sync);
  const syncMatch = path.match(/^\/api\/admin\/sync\/(\w+)$/);
  if (syncMatch && method === "POST") {
    const state = sync.find((s) => s.source === syncMatch[1])!;
    state.status = "waiting";
    state.requested = true;
    return json({ queued: true }, 202);
  }
  if (path === "/api/admin/posts" && method === "GET") return json(Object.values(posts).map(view));
  if (path === "/api/admin/posts" && method === "POST") {
    const input = body as { title: string; markdown: string; creationKey: string };
    const payload = JSON.stringify([input.title, input.markdown]);
    const existing = creations[input.creationKey];
    if (existing) {
      if (existing.payload !== payload) return json({ error: "conflict" }, 409);
      return json(view(posts[existing.postId]), 201);
    }
    const postId = (nextId++).toString().padStart(43, "n");
    posts[postId] = { id: postId, title: input.title, markdown: input.markdown, version: 1, published: false, publishedAt: null, updatedAt: stamp(), hasUnpublishedChanges: true };
    creations[input.creationKey] = { postId, payload };
    return json(view(posts[postId]), 201);
  }
  const creation = path.match(/^\/api\/admin\/creation-results\/(.+)$/);
  if (creation) {
    const found = creations[decodeURIComponent(creation[1])];
    return found ? json(view(posts[found.postId])) : json({ error: "not found" }, 404);
  }
  const article = path.match(/^\/api\/admin\/posts\/([^/]+)(\/publish|\/unpublish)?$/);
  if (article) {
    const current = posts[article[1]];
    if (!current) return json({ error: "not found" }, 404);
    if (method === "GET") return json(view(current));
    const input = body as { title: string; markdown: string; version: number };
    if (input.version !== current.version) return json({ error: "別の変更が保存されています。" }, 409);
    if (article[2] === "/publish") {
      if (!current.title.trim()) return json({ error: "公開するにはタイトルを入力してください。" }, 400);
      published[current.id] = { title: current.title, markdown: current.markdown };
      posts[current.id] = { ...current, published: true, publishedAt: current.publishedAt ?? stamp(), version: current.version + 1, updatedAt: stamp() };
      return json(view(posts[current.id]));
    }
    if (article[2] === "/unpublish") {
      posts[current.id] = { ...current, published: false, version: current.version + 1, updatedAt: stamp() };
      return json(view(posts[current.id]));
    }
    if (/blog-image:v1;width=(?!auto|[1-9]\d|100)/.test(input.markdown)) return json({ error: "画像の表示設定（blog-image:v1）の形式が正しくありません。" }, 400);
    posts[current.id] = { ...current, title: input.title, markdown: input.markdown, version: current.version + 1, updatedAt: stamp() };
    return json(view(posts[current.id]));
  }
  if (path === "/api/admin/image-uploads" && method === "POST") {
    const input = body as Upload["request"];
    let upload = Object.values(uploads).find((u) => u.request.clientImageId === input.clientImageId);
    if (!upload) {
      upload = { id: `up${nextId++}`.padEnd(43, "u"), request: input, status: "pending", received: false };
      uploads[upload.id] = upload;
    }
    if (upload.status === "completed") return json({ uploadId: upload.id, status: "completed", result: upload.result });
    return json({ uploadId: upload.id, status: upload.status, upload: { method: "PUT", url: `/api/admin/image-uploads/${upload.id}/content`, expiresAt: "" } });
  }
  const uploadMatch = path.match(/^\/api\/admin\/image-uploads\/([^/]+)(\/complete|\/content)?$/);
  if (uploadMatch) {
    const upload = uploads[uploadMatch[1]];
    if (!upload) return json({ error: "not found" }, 404);
    if (uploadMatch[2] === "/content") {
      upload.received = true;
      return json({ stored: true });
    }
    if (uploadMatch[2] === "/complete") {
      if (upload.status === "completed") return json({ uploadId: upload.id, status: "completed", result: upload.result });
      if (!upload.received) return json({ uploadId: upload.id, status: "pending", failureCode: "not_uploaded", error: "未送信" }, 409);
      const failure = server.uploadFailures[upload.request.fileName];
      if (failure) {
        upload.status = "failed";
        return json({ uploadId: upload.id, status: "failed", failureCode: failure, error: failure }, 400);
      }
      upload.status = "completed";
      server.imagesRegistered++;
      upload.result = { id: `img${upload.id}`.slice(0, 43).padEnd(43, "i"), url: `/api/images/${`img${upload.id}`.slice(0, 43).padEnd(43, "i")}`, width: 2027, height: 254 };
      return json({ uploadId: upload.id, status: "completed", result: upload.result });
    }
    return json({ uploadId: upload.id, status: upload.status, result: upload.result });
  }
  throw new Error(`Unexpected fixture request: ${method} ${path}`);
}

window.fetch = async (input, init) => {
  const path = String(input);
  const method = init?.method || "GET";
  server.calls.push(`${method} ${path}`);
  let body: unknown = undefined;
  if (typeof init?.body === "string") body = JSON.parse(init.body);
  server.bodies.push({ method, path, body });
  const index = rules.findIndex((rule) => rule.method === method && path.endsWith(rule.path));
  const rule = index >= 0 ? rules[index] : undefined;
  if (rule) {
    rule.times = (rule.times ?? 1) - 1;
    if (rule.times <= 0) rules.splice(index, 1);
    if (rule.mode === "before") throw new TypeError("fixture: network failure before commit");
    if (rule.mode === "status") return json(rule.body ?? { error: "fixture failure" }, rule.status ?? 500, rule.headers);
    if (rule.mode === "hold") await new Promise<void>((resolve) => held.push(resolve));
  }
  const response = await handle(method, path, body);
  if (rule?.mode === "lost") throw new TypeError("fixture: response lost after commit");
  return response;
};

// ---- screens --------------------------------------------------------------
function Layout() {
  return (
    <>
      <header className="site-header">
        <Link to="/" search={{ page: 1 }} className="brand" aria-label="course_kのブログのトップへ">
          <img src="/brand/mark.svg" width="35" height="28" alt="" />
        </Link>
        <nav aria-label="メイン">
          <Link to="/" search={{ page: 1 }}>
            記事
          </Link>
          <Link to="/admin" search={{ view: "new" }}>
            管理
          </Link>
        </nav>
      </header>
      <main id="main">
        <Outlet />
      </main>
      <footer>
        course_k<span>検証専用の架空データ</span>
      </footer>
    </>
  );
}
function Home() {
  if (cfg.screen === "list")
    return (
      <>
        <ArticleList
          posts={cfg.empty ? [] : [post, { ...post, id: "b".repeat(43), title: "検証用の記事：日々の記録を残す" }]}
          page={1}
          months={cfg.empty ? [] : [{ month: "2026-10", count: 22 }, { month: "2025-12", count: 3 }]}
        />
        <section aria-label="外部記事部品の検証" style={{ maxWidth: 680, marginInline: "auto" }}>
          <ArticleCard
            article={{ kind: "external", id: "q", title: "外部記事の表示検証", source: "qiita", url: "https://example.com/article", publishedAt: post.publishedAt }}
          />
        </section>
      </>
    );
  return <Article post={post} />;
}
const rootRoute = createRootRoute({ component: Layout });
const homeRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  validateSearch: (search: Record<string, unknown>) => ({ ...search, page: Number(search.page) || 1 }),
  search: { middlewares: [stripSearchParams({ page: 1 })] },
  component: Home,
});
const adminRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/admin",
  validateSearch: validateAdminSearch,
  component: function Admin() {
    return <AdminPage search={adminRoute.useSearch()} />;
  },
});
const router = createRouter({
  routeTree: rootRoute.addChildren([homeRoute, adminRoute]),
  history: createMemoryHistory({ initialEntries: [cfg.screen === "admin" ? (cfg.path ?? "/admin") : "/"] }),
});
(window as unknown as { router: typeof router }).router = router;
createRoot(document.getElementById("root")!).render(<RouterProvider router={router} />);
