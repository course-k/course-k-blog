import { useBlocker } from "@tanstack/react-router";
import { Markdown } from "./Markdown";
import { useAdminEditor } from "../features/editor/useAdminEditor";

export function AdminPage() {
  const {
    signedIn,
    password,
    setPassword,
    posts,
    selectedPost,
    title,
    setTitle,
    markdown,
    setMarkdown,
    busy,
    message,
    error,
    editing,
    mode,
    setMode,
    uncertain,
    dirty,
    openPost,
    confirmLeaveEditor,
    login,
    logout,
    saveDraft,
    publish,
    unpublish,
    checkResult,
  } = useAdminEditor();
  useBlocker({
    shouldBlockFn: () => !confirmLeaveEditor(),
    enableBeforeUnload: dirty || uncertain || busy,
  });
  if (signedIn === null)
    return (
      <p className="notice" role="status">
        ログイン状態を確認しています…
      </p>
    );
  if (!signedIn)
    return (
      <section className="login">
        <p className="eyebrow">PRIVATE</p>
        <h1>管理者ログイン</h1>
        <p className="muted">記事を書いたり、公開状態を管理できます。</p>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void login();
          }}
        >
          <label htmlFor="password">パスワード</label>
          <input
            id="password"
            type="password"
            autoComplete="current-password"
            required
            maxLength={1024}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <button disabled={busy}>{busy ? "確認中…" : "ログイン"}</button>
        </form>
        {error && (
          <p role="alert" className="error">
            {error}
          </p>
        )}
      </section>
    );
  return (
    <section className="admin">
      <div className="admin-heading">
        <div>
          <p className="eyebrow">WORKSPACE</p>
          <h1>記事を書く</h1>
        </div>
        <button
          className="secondary"
          disabled={busy}
          onClick={() => void logout()}
        >
          ログアウト
        </button>
      </div>
      <div className="editor-layout">
        <aside>
          <button disabled={busy} onClick={() => void openPost(null)}>
            ＋ 新しい記事
          </button>
          <ul className="draft-list">
            {posts.map((p) => (
              <li key={p.id}>
                <button
                  className={selectedPost?.id === p.id ? "draft active" : "draft"}
                  disabled={busy}
                  onClick={() => void openPost(p)}
                >
                  <span>{p.title}</span>
                  <small>
                    {p.published ? "公開中" : "下書き"}
                    {p.published && p.hasUnpublishedChanges
                      ? "・未公開の変更あり"
                      : ""}
                  </small>
                </button>
              </li>
            ))}
          </ul>
        </aside>
        <div>
          {editing ? (
            <>
              <p className="editor-state">
                {selectedPost?.published
                  ? selectedPost.hasUnpublishedChanges
                    ? "公開中・保存済みの未公開変更あり"
                    : "公開中"
                  : "下書き・読者には表示されません"}
              </p>
              <label htmlFor="title">タイトル</label>
              <input
                id="title"
                maxLength={200}
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                disabled={busy}
              />
              <div className="editor-mode" aria-label="編集表示">
                <button
                  type="button"
                  className="secondary"
                  aria-pressed={mode === "write"}
                  onClick={() => setMode("write")}
                >
                  編集
                </button>
                <button
                  type="button"
                  className="secondary"
                  aria-pressed={mode === "preview"}
                  onClick={() => setMode("preview")}
                >
                  プレビュー
                </button>
              </div>
              <div className="editor-columns" data-mode={mode}>
                <div className="markdown-input">
                  <label htmlFor="markdown">Markdown</label>
                  <textarea
                    id="markdown"
                    value={markdown}
                    onChange={(e) => setMarkdown(e.target.value)}
                    disabled={busy}
                    spellCheck={false}
                  />
                </div>
                <section className="preview" aria-label="プレビュー">
                  <h2>プレビュー</h2>
                  <Markdown
                    text={markdown || "ここにプレビューが表示されます。"}
                  />
                </section>
              </div>
              <div className="actions">
                <button
                  disabled={busy || uncertain || !title.trim()}
                  onClick={() => void saveDraft()}
                >
                  下書きを保存
                </button>
                <button
                  className="secondary"
                  disabled={busy || uncertain || !title.trim()}
                  onClick={() => void publish()}
                >
                  {selectedPost?.published ? "更新を公開する" : "公開する"}
                </button>
                {selectedPost?.published && (
                  <button
                    className="quiet"
                    disabled={busy || uncertain}
                    onClick={() => void unpublish()}
                  >
                    非公開に戻す
                  </button>
                )}
              </div>
              <p className="muted" role="status" aria-live="polite">
                {busy
                  ? message || "処理しています…"
                  : message ||
                    (dirty
                      ? "保存していない変更があります。"
                      : "編集内容は自動保存されません。")}
              </p>
              {uncertain && (
                <button
                  className="secondary"
                  disabled={busy}
                  onClick={() => void checkResult()}
                >
                  保存結果を確認
                </button>
              )}
            </>
          ) : (
            <div className="empty">
              <h2>書きたいことを、ここから。</h2>
              <p>記事を選ぶか、新しい記事を作成してください。</p>
            </div>
          )}
          {error && (
            <p className="error" role="alert">
              {error}
            </p>
          )}
        </div>
      </div>
    </section>
  );
}
