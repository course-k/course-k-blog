import { useEffect, useState } from "react";
import type { Draft } from "../../lib/types";
import { adminApi, ApiError } from "./adminApi";

// Owns the editor workflow; the Go API remains authoritative for publication.
export function useAdminEditor() {
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
    adminApi
      .session()
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
      adminApi
        .list()
        .then(setPosts)
        .catch((e) => setError(e.message));
  }, [signedIn]);
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
      setPosts(await adminApi.list());
    } catch {
      setError(
        "記事への操作は完了しましたが、記事一覧を更新できませんでした。入力内容は保持しています。",
      );
    }
  }
  async function checkResult() {
    const current = await adminApi.list();
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
        ? await adminApi.save(selected.id, title, markdown, selected.version)
        : await adminApi.create(title, markdown);
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
      published = await adminApi.publish(p.id, p.version);
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

  async function login() {
    await adminApi.login(password);
    setPassword("");
    setSignedIn(true);
  }
  async function logout() {
    if (dirty && !window.confirm("未保存の変更を破棄してログアウトしますか？"))
      return;
    await act(async () => {
      await adminApi.logout();
      setSignedIn(false);
      setPosts([]);
      setSelected(null);
      setEditing(false);
      setTitle("");
      setMarkdown("");
    });
  }
  async function saveDraft() {
    const saved = await save();
    await refresh(saved);
    setMessage("下書きを保存しました。公開中の記事は変わりません。");
  }
  async function unpublish() {
    if (!selected) return;
    if (!window.confirm("この記事を非公開に戻しますか？")) return;
    await act(async () => {
      try {
        await refresh(await adminApi.unpublish(selected.id, selected.version));
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
  }
  return {
    signedIn,
    password,
    setPassword,
    posts,
    selected,
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
    choose,
    logout,
    login: () => act(login),
    saveDraft: () => act(saveDraft),
    publish: () => act(publish),
    unpublish,
    checkResult: () => act(checkResult),
  };
}
