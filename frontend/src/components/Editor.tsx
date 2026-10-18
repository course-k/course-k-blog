import { useBlocker } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { Markdown } from "./Markdown";
import type { Draft } from "../lib/types";

class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}
async function api<T>(
  path: string,
  method = "GET",
  body?: unknown,
): Promise<T> {
  const response = await fetch(`/api${path}`, {
    method,
    credentials: "same-origin",
    cache: "no-store",
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await response.json();
  if (!response.ok)
    throw new ApiError(response.status, data.error || "処理できませんでした。");
  return data;
}
export function Admin() {
  const [signedIn, setSignedIn] = useState<boolean | null>(null);
  const [password, setPassword] = useState("");
  const [posts, setPosts] = useState<Draft[]>([]);
  const [selected, setSelected] = useState<Draft | null>(null);
  const [title, setTitle] = useState("");
  const [markdown, setMarkdown] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [editing, setEditing] = useState(false);
  const [mode, setMode] = useState<"write" | "preview">("write");
  const [uncertain, setUncertain] = useState(false);
  const dirty =
    editing &&
    (selected
      ? title !== selected.title || markdown !== selected.markdown
      : title !== "" || markdown !== "");
  useEffect(() => {
    let active = true;
    api("/session")
      .then(() => {
        if (active) setSignedIn(true);
      })
      .catch((e) => {
        if (active) {
          setSignedIn(false);
          if (!(e instanceof ApiError && e.status === 401))
            setError("接続を確認できませんでした。再読み込みしてください。");
        }
      });
    return () => {
      active = false;
    };
  }, []);
  useEffect(() => {
    if (signedIn)
      api<Draft[]>("/admin/posts")
        .then(setPosts)
        .catch((e) => setError(e.message));
  }, [signedIn]);
  useBlocker({
    shouldBlockFn: () =>
      busy ||
      ((dirty || uncertain) &&
        !window.confirm(
          "保存していない変更、または確認できていない操作結果があります。このページを離れますか？",
        )),
    enableBeforeUnload: dirty || uncertain || busy,
  });
  async function act(fn: () => Promise<void>) {
    setBusy(true);
    setError("");
    setMessage("");
    try {
      await fn();
    } catch (e) {
      setError(e instanceof Error ? e.message : "処理できませんでした。");
      if (e instanceof ApiError && e.status === 401) setSignedIn(false);
    } finally {
      setBusy(false);
    }
  }
  function choose(post: Draft | null) {
    if (dirty && !window.confirm("保存していない変更を破棄しますか？")) return;
    setSelected(post);
    setTitle(post?.title || "");
    setMarkdown(post?.markdown || "");
    setEditing(true);
    setMode("write");
    setUncertain(false);
    setMessage("");
    setError("");
  }
  async function refresh(post: Draft) {
    setSelected(post);
    setPosts((current) => [post, ...current.filter((p) => p.id !== post.id)]);
    try {
      setPosts(await api<Draft[]>("/admin/posts"));
    } catch {
      setError(
        "記事への操作は完了しましたが、記事一覧を更新できませんでした。入力内容は保持しています。",
      );
    }
  }
  async function checkResult() {
    const current = await api<Draft[]>("/admin/posts");
    setPosts(current);
    const matches = selected ? current.filter((p) => p.id === selected.id) : [];
    if (matches.length === 1) {
      setSelected(matches[0]);
      setUncertain(false);
      setMessage(
        "保存された記事の状態を確認しました。公開状態を確認してから操作してください。",
      );
    } else {
      setError(
        "記事一覧を再取得しました。保存先を特定できないため、内容を確認して対象の記事を一覧から選んでください。重複作成を防ぐため再送は止めています。",
      );
    }
  }
  async function save(): Promise<Draft> {
    let result: Draft;
    try {
      result = selected
        ? await api<Draft>(`/admin/posts/${selected.id}`, "PUT", {
            title,
            markdown,
            version: selected.version,
          })
        : await api<Draft>("/admin/posts", "POST", { title, markdown });
    } catch (e) {
      if (!(e instanceof ApiError)) {
        setUncertain(true);
        throw new Error(
          "通信が途切れ、保存結果を確認できません。入力内容は保持しています。「保存結果を確認」で確認してください。",
        );
      }
      if (e.status === 409)
        throw new Error(
          "別の更新と競合しました。入力内容は保持しています。記事を開き直す前に内容をコピーしてください。",
        );
      throw e;
    }
    setSelected(result);
    setPosts((current) => [
      result,
      ...current.filter((p) => p.id !== result.id),
    ]);
    return result;
  }
  async function publish() {
    const p = await save();
    setMessage("下書きは保存済みです。公開処理を進めています…");
    let published: Draft;
    try {
      published = await api<Draft>(`/admin/posts/${p.id}/publish`, "POST", {
        version: p.version,
      });
    } catch (e) {
      if (!(e instanceof ApiError)) setUncertain(true);
      setMessage("下書きは保存済みです。");
      if (e instanceof ApiError && e.status === 401) {
        setSignedIn(false);
        throw e;
      }
      throw new Error(
        e instanceof ApiError
          ? "下書きは保存しましたが、公開できませんでした。以前の公開版は維持されます。再度公開してください。"
          : "下書きは保存済みですが、公開結果を確認できません。「保存結果を確認」で確認してください。",
      );
    }
    await refresh(published);
    setMessage("公開しました。");
  }

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
            void act(async () => {
              await api("/session", "POST", { password });
              setPassword("");
              setSignedIn(true);
            });
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
          onClick={() => {
            if (
              dirty &&
              !window.confirm("未保存の変更を破棄してログアウトしますか？")
            )
              return;
            void act(async () => {
              await api("/session", "DELETE", {});
              setSignedIn(false);
              setPosts([]);
              setSelected(null);
              setEditing(false);
              setTitle("");
              setMarkdown("");
            });
          }}
        >
          ログアウト
        </button>
      </div>
      <div className="editor-layout">
        <aside>
          <button disabled={busy} onClick={() => choose(null)}>
            ＋ 新しい記事
          </button>
          <ul className="draft-list">
            {posts.map((p) => (
              <li key={p.id}>
                <button
                  className={selected?.id === p.id ? "draft active" : "draft"}
                  disabled={busy}
                  onClick={() => choose(p)}
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
                {selected?.published
                  ? selected.hasUnpublishedChanges
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
                  onClick={() =>
                    void act(async () => {
                      const saved = await save();
                      await refresh(saved);
                      setMessage(
                        "下書きを保存しました。公開中の記事は変わりません。",
                      );
                    })
                  }
                >
                  下書きを保存
                </button>
                <button
                  className="secondary"
                  disabled={busy || uncertain || !title.trim()}
                  onClick={() => void act(publish)}
                >
                  {selected?.published ? "更新を公開する" : "公開する"}
                </button>
                {selected?.published && (
                  <button
                    className="quiet"
                    disabled={busy || uncertain}
                    onClick={() => {
                      if (!window.confirm("この記事を非公開に戻しますか？"))
                        return;
                      void act(async () => {
                        try {
                          await refresh(
                            await api<Draft>(
                              `/admin/posts/${selected.id}/unpublish`,
                              "POST",
                              { version: selected.version },
                            ),
                          );
                          setMessage("非公開にしました。");
                        } catch (e) {
                          if (!(e instanceof ApiError)) {
                            setUncertain(true);
                            throw new Error(
                              "非公開への変更結果を確認できません。「保存結果を確認」で公開状態を確認してください。",
                            );
                          }
                          throw e;
                        }
                      });
                    }}
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
                  onClick={() => void act(checkResult)}
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
