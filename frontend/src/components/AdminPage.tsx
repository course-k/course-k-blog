import { useCallback, useEffect, useMemo, useReducer, useRef, useState, type ReactNode } from "react";
import { Link, useBlocker, useNavigate } from "@tanstack/react-router";
import { useAdminAuth } from "../features/editor/useAdminAuth";
import { validArticleId, type AdminSearch } from "../features/editor/adminSearch";
import {
  createRecoveryStore,
  holdTabLock,
  liveTabSessions,
  recoverableRecords,
  type RecoveryRecord,
} from "../features/editor/draftRecovery";
import type { AdminPost } from "../lib/types";
import { ArticleEditor, type EditorHandle } from "./ArticleEditor";
import { AdminArticleList } from "./AdminArticleList";
import { AdminSettings } from "./AdminSettings";

export function AdminPage({ search }: { search: AdminSearch }) {
  const auth = useAdminAuth();
  let loginScreen: ReactNode = null;
  if (auth.signedIn === null)
    loginScreen = (
      <p className="notice" role="status">
        ログイン状態を確認しています…
        {auth.slowStart && " データベースの起動を待っています。1分ほどかかることがあります。"}
      </p>
    );
  else if (!auth.signedIn && auth.authMode === "loading")
    loginScreen = <p className="notice">ログイン方法を確認しています…</p>;
  else if (!auth.signedIn && auth.authMode === "unavailable")
    loginScreen = (
      <section className="login">
        <h1>管理者ログイン</h1>
        <p role="alert">ログイン方法を取得できませんでした。</p>
        <button onClick={() => window.location.reload()}>再読み込み</button>
      </section>
    );
  else if (!auth.signedIn && auth.authMode === "oidc")
    loginScreen = (
      <section className="login">
        <h1>管理者ログイン</h1>
        <p>
          専用のログイン画面で、ブログ用アカウントと追加認証を確認します。編集中の入力は元の画面に保持します。
        </p>
        <button disabled={auth.busy} onClick={() => void auth.loginWithProvider()}>
          {auth.busy ? "ログインを待っています…" : "専用画面でログイン"}
        </button>
        {auth.busy && (
          <button className="secondary" onClick={auth.cancelProviderLogin}>
            ログイン確認を中断
          </button>
        )}
        {auth.error && (
          <p className="error" role="alert">
            {auth.error}
          </p>
        )}
      </section>
    );
  else if (!auth.signedIn)
    loginScreen = (
      <section className="login">
        <p className="eyebrow">PRIVATE</p>
        <h1>管理者ログイン</h1>
        {auth.hasWorkspace && <p className="muted">再ログインが必要です。編集中の入力は保持しています。</p>}
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void auth.login();
          }}
        >
          <label htmlFor="password">パスワード</label>
          <input
            id="password"
            type="password"
            autoComplete="current-password"
            required
            maxLength={1024}
            value={auth.password}
            onChange={(event) => auth.setPassword(event.target.value)}
          />
          <button disabled={auth.busy}>{auth.busy ? "確認中…" : "ログイン"}</button>
        </form>
        {auth.error && (
          <p role="alert" className="error">
            {auth.error}
          </p>
        )}
      </section>
    );
  return (
    <>
      {loginScreen}
      {/* Drafts are never rendered before authentication (including SSR). On
          expiry the workspace is hidden, not unmounted, so input survives. */}
      {auth.hasWorkspace && (
        <div hidden={auth.signedIn !== true}>
          <AdminShell
            search={search}
            loginCount={auth.loginCount}
            onAuthExpired={auth.requestLogin}
            logout={auth.logout}
            leavingForLogout={auth.leavingForLogout}
          />
        </div>
      )}
    </>
  );
}

type EditorSession = { key: number; articleId: string | null; restore?: RecoveryRecord };

function AdminShell({
  search,
  loginCount,
  onAuthExpired,
  logout,
  leavingForLogout,
}: {
  search: AdminSearch;
  loginCount: number;
  onAuthExpired(): void;
  logout(afterSignedOut?: () => Promise<boolean>): Promise<boolean>;
  leavingForLogout: React.RefObject<boolean>;
}) {
  const navigate = useNavigate();
  const store = useMemo(() => createRecoveryStore(), []);
  const [storageUnavailable, setStorageUnavailable] = useState(false);
  const [recoverable, setRecoverable] = useState<RecoveryRecord[]>([]);
  const editor = useRef<EditorHandle | null>(null);
  const counter = useRef(0);
  const sessionRef = useRef<EditorSession | null>(null);
  // A copy chosen for restoring waits here until its editor is created, so a
  // render of another screen in between cannot drop it.
  const pendingRestore = useRef<RecoveryRecord | null>(null);
  const allowNext = useRef(false);
  const pendingLeave = useRef<Promise<boolean> | null>(null);
  const [, rerender] = useReducer((n: number) => n + 1, 0);
  const [dropNotice, setDropNotice] = useState("");

  // On every admin screen, files dropped outside the article body must not
  // make the browser open them. The body textarea handles its own drops.
  useEffect(() => {
    const guard = (event: DragEvent) => {
      if (!event.dataTransfer?.types.includes("Files")) return;
      if (event.target instanceof Element && event.target.closest("#markdown")) return;
      event.preventDefault();
      if (event.type === "drop") setDropNotice("画像は本文へドロップしてください。");
    };
    window.addEventListener("dragover", guard);
    window.addEventListener("drop", guard);
    return () => {
      window.removeEventListener("dragover", guard);
      window.removeEventListener("drop", guard);
    };
  }, []);

  useEffect(() => {
    holdTabLock();
    if (!store) {
      setStorageUnavailable(true);
      return;
    }
    let active = true;
    void (async () => {
      try {
        const records = await recoverableRecords(store, Date.now(), await liveTabSessions());
        if (active) setRecoverable(records);
      } catch {
        if (active) setStorageUnavailable(true);
      }
    })();
    return () => {
      active = false;
    };
  }, [store]);

  // The editor session follows the URL. Choosing "新規作成" again on an
  // unsaved new article keeps it; a created article keeps its session when the
  // URL is replaced with its ID.
  const current = sessionRef.current;
  let session: EditorSession | null = null;
  if (search.view === "new") session = current && current.articleId === null ? current : null;
  else if (search.view === "edit" && validArticleId(search.id))
    session = current && current.articleId === search.id ? current : null;
  if ((search.view === "new" || (search.view === "edit" && validArticleId(search.id))) && !session) {
    const articleId = search.view === "edit" ? search.id! : null;
    const restoring = pendingRestore.current?.articleId === articleId ? pendingRestore.current : null;
    if (restoring) pendingRestore.current = null;
    session = { key: ++counter.current, articleId, restore: restoring ?? undefined };
  }
  sessionRef.current = session;

  const onCreated = useCallback(
    (post: AdminPost) => {
      if (sessionRef.current) sessionRef.current.articleId = post.id;
      allowNext.current = true;
      // Replace, not push: the new article's URL without an extra history entry.
      void navigate({ to: "/admin", search: { view: "edit", id: post.id }, replace: true });
    },
    [navigate],
  );

  const leaveEditor = useCallback(async () => {
    if (!editor.current) return true;
    pendingLeave.current ??= editor.current.prepareLeave().finally(() => {
      pendingLeave.current = null;
    });
    return pendingLeave.current;
  }, []);

  useBlocker({
    shouldBlockFn: async ({ current: from, next }) => {
      if (allowNext.current) {
        allowNext.current = false;
        return false;
      }
      const nextSearch = next.search as Partial<AdminSearch>;
      const fromSearch = from.search as Partial<AdminSearch>;
      const sameEditor =
        next.pathname === from.pathname &&
        (nextSearch.view ?? "new") === (fromSearch.view ?? "new") &&
        nextSearch.id === fromSearch.id;
      if (sameEditor || !editor.current) return false;
      // While a save for leaving is pending, further navigation waits for it.
      if (pendingLeave.current) return true;
      return !(await leaveEditor());
    },
    enableBeforeUnload: () => !leavingForLogout.current && Boolean(editor.current?.hasUnsavedWork()),
  });

  async function restore(record: RecoveryRecord) {
    if (!(await leaveEditor())) return;
    setRecoverable((records) => records.filter((item) => item.key !== record.key));
    pendingRestore.current = record;
    // Never reuse the current editor (e.g. an empty new article) for a restore.
    sessionRef.current = null;
    allowNext.current = true;
    await navigate({
      to: "/admin",
      search: record.articleId ? { view: "edit", id: record.articleId } : { view: "new" },
    });
    allowNext.current = false;
    rerender();
  }

  async function discard(record: RecoveryRecord) {
    try {
      await store?.delete(record.key);
    } catch {
      setStorageUnavailable(true);
    }
    setRecoverable((records) => records.filter((item) => item.key !== record.key));
  }

  async function onLogout() {
    // One owner: a successful explicit logout removes this app's local copies,
    // and the provider logout starts only after that deletion completed.
    return logout(async () => {
      if (!store) return true;
      try {
        await store.clear();
        return true;
      } catch {
        return false;
      }
    });
  }

  const menu: { view: AdminSearch["view"]; label: string }[] = [
    { view: "new", label: "新規作成" },
    { view: "articles", label: "記事一覧" },
    { view: "settings", label: "設定" },
  ];
  const activeMenu = search.view === "edit" ? "articles" : search.view;

  return (
    <div className="admin-shell">
      <nav className="admin-nav" aria-label="管理メニュー">
        <ul>
          {menu.map((item) => (
            <li key={item.view}>
              <Link
                to="/admin"
                search={{ view: item.view }}
                aria-current={activeMenu === item.view ? "page" : undefined}
              >
                {item.label}
              </Link>
            </li>
          ))}
        </ul>
      </nav>
      <div className="admin-main">
        {dropNotice && (
          <p className="error" role="alert">
            {dropNotice}
          </p>
        )}
        {storageUnavailable && (
          <p className="warning" role="status">
            この端末への一時保存ができません。通信切断後の復元はできません。
          </p>
        )}
        {recoverable.length > 0 && (
          <section className="recovery-prompt" aria-labelledby="recovery-heading">
            <h2 id="recovery-heading">前回の編集内容を復元しますか？</h2>
            <ul>
              {recoverable.map((record) => (
                <li key={record.key}>
                  <span>
                    <strong>{record.doc.title || "無題"}</strong>
                    <small>
                      {new Date(record.updatedAt).toLocaleString("ja-JP", {
                        timeZone: "Asia/Tokyo",
                        year: "numeric",
                        month: "2-digit",
                        day: "2-digit",
                        hour: "2-digit",
                        minute: "2-digit",
                      })}
                    </small>
                  </span>
                  <button type="button" className="secondary" onClick={() => void restore(record)}>
                    復元
                  </button>
                  <button type="button" className="quiet" onClick={() => void discard(record)}>
                    破棄
                  </button>
                </li>
              ))}
            </ul>
          </section>
        )}
        {session ? (
          <ArticleEditor
            key={session.key}
            ref={editor}
            articleId={session.articleId}
            restore={session.restore}
            store={store}
            loginCount={loginCount}
            onCreated={onCreated}
            onAuthExpired={onAuthExpired}
            onStorageUnavailable={() => setStorageUnavailable(true)}
          />
        ) : search.view === "edit" ? (
          <section className="editor-missing" role="alert">
            <h1>記事を開けませんでした</h1>
            <p>記事のIDが正しくありません。</p>
            <Link to="/admin" search={{ view: "articles" }}>
              記事一覧へ
            </Link>
          </section>
        ) : search.view === "articles" ? (
          <AdminArticleList onAuthExpired={onAuthExpired} />
        ) : (
          <AdminSettings onAuthExpired={onAuthExpired} onLogout={onLogout} />
        )}
      </div>
    </div>
  );
}
