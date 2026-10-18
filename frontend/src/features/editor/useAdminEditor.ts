import { useEffect, useRef, useState } from "react";
import type { AdminPost } from "../../lib/types";
import { adminApi, ApiError, outcomeIsUnknown } from "./adminApi";

type WriteAction = "save" | "publish" | "unpublish";
type EditorAction = WriteAction | "login" | "logout" | "open" | "check-result";
type OperationState =
  | { status: "idle" }
  | { status: "running"; action: EditorAction }
  | { status: "unknown"; action: WriteAction; postId: string | null };

const actionNames: Record<WriteAction, string> = {
  save: "保存", publish: "公開", unpublish: "非公開への変更",
};

// Go owns publication. This hook owns local input and recovery after uncertain writes.
export function useAdminEditor() {
  const [signedIn, setSignedIn] = useState<boolean | null>(null);
  const [password, setPassword] = useState("");
  const [posts, setPosts] = useState<AdminPost[]>([]);
  const [selectedPost, setSelectedPost] = useState<AdminPost | null>(null);
  const [title, setTitle] = useState("");
  const [markdown, setMarkdown] = useState("");
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [editing, setEditing] = useState(false);
  const [mode, setMode] = useState<"write" | "preview">("write");
  const [operation, setOperation] = useState<OperationState>({ status: "idle" });
  const inFlight = useRef(false);
  const busy = operation.status === "running";
  const uncertain = operation.status === "unknown";
  const dirty = editing && (
    title !== (selectedPost?.title ?? "") || markdown !== (selectedPost?.markdown ?? "")
  );

  useEffect(() => {
    let active = true;
    adminApi.session().then(() => {
      if (active) setSignedIn(true);
    }).catch((error: unknown) => {
      if (!active) return;
      setSignedIn(false);
      if (!(error instanceof ApiError && error.status === 401)) {
        setError("接続を確認できませんでした。再読み込みしてください。");
      }
    });
    return () => { active = false; };
  }, []);

  useEffect(() => {
    if (!signedIn) return;
    let active = true;
    adminApi.list().then(posts => {
      if (active) setPosts(posts);
    }).catch((error: unknown) => {
      if (active) setError(error instanceof Error ? error.message : "一覧を取得できませんでした。");
    });
    return () => { active = false; };
  }, [signedIn]);

  async function runEditorAction(action: EditorAction, execute: () => Promise<void>) {
    if (inFlight.current) return;
    const previousOperation = operation;
    inFlight.current = true;
    setOperation({ status: "running", action });
    setError("");
    setMessage("");
    try {
      await execute();
    } catch (error) {
      setError(error instanceof Error ? error.message : "処理できませんでした。");
      if (error instanceof ApiError && error.status === 401) setSignedIn(false);
    } finally {
      // A failed refresh or re-login must not clear a preceding unknown outcome.
      setOperation(current => current.status === "running" ? previousOperation : current);
      inFlight.current = false;
    }
  }

  function confirmLeaveEditor() {
    if (busy || inFlight.current) return false;
    if (!dirty && !uncertain) return true;
    return window.confirm(uncertain
      ? "操作結果を確認できていません。サーバーでは成功している可能性があります。入力を破棄して移動しますか？公開状態は改めて確認してください。"
      : "保存していない変更を破棄しますか？");
  }

  function applySavedPost(post: AdminPost) {
    setSelectedPost(post);
    setPosts(current => [post, ...current.filter(existing => existing.id !== post.id)]);
  }

  async function reloadPostList() {
    try {
      setPosts(await adminApi.list());
    } catch {
      setError("記事への操作は完了しましたが、記事一覧を更新できませんでした。入力内容は保持しています。");
    }
  }

  async function openPost(post: AdminPost | null) {
    if (!confirmLeaveEditor()) return;
    await runEditorAction("open", async () => {
      // List snapshots can be stale, especially after a lost publication response.
      const latestPost = post ? await adminApi.get(post.id) : null;
      if (latestPost) applySavedPost(latestPost);
      else setSelectedPost(null);
      setTitle(latestPost?.title ?? "");
      setMarkdown(latestPost?.markdown ?? "");
      setEditing(true);
      setMode("write");
      setOperation({ status: "idle" });
    });
  }

  async function checkResult() {
    if (operation.status !== "unknown") return;
    const pendingPostId = operation.postId;
    await runEditorAction("check-result", async () => {
      if (!pendingPostId) {
        setPosts(await adminApi.list());
        setOperation(operation);
        setError("記事一覧を再取得しました。保存先を特定できないため、内容を確認して対象の記事を一覧から選んでください。重複作成を防ぐため再送は止めています。");
        return;
      }
      applySavedPost(await adminApi.get(pendingPostId));
      setOperation({ status: "idle" });
      setMessage("保存された記事の状態を確認しました。入力内容は保持しています。公開状態を確認してから操作してください。");
    });
  }

  async function writePost(action: WriteAction, postId: string | null, execute: () => Promise<AdminPost>) {
    try {
      return await execute();
    } catch (error) {
      if (outcomeIsUnknown(error)) {
        setOperation({ status: "unknown", action, postId });
        throw new Error(`${actionNames[action]}結果を確認できません。入力内容は保持しています。「保存結果を確認」で確認してください。`);
      }
      if (error instanceof ApiError && error.status === 409) {
        throw new Error("別の更新と競合しました。入力内容は保持しています。記事を開き直す前に内容をコピーしてください。");
      }
      throw error;
    }
  }

  async function saveWorkingCopy() {
    const savedPost = await writePost("save", selectedPost?.id ?? null, () => selectedPost
      ? adminApi.save(selectedPost.id, title, markdown, selectedPost.version)
      : adminApi.create(title, markdown));
    applySavedPost(savedPost);
    return savedPost;
  }

  async function saveDraft() {
    await saveWorkingCopy();
    await reloadPostList();
    setMessage("下書きを保存しました。公開中の記事は変わりません。");
  }

  async function publish() {
    const savedPost = await saveWorkingCopy();
    setMessage("下書きは保存済みです。公開処理を進めています…");
    try {
      applySavedPost(await writePost("publish", savedPost.id, () => adminApi.publish(savedPost.id, savedPost.version)));
    } catch (error) {
      setMessage("下書きは保存済みです。");
      throw error;
    }
    await reloadPostList();
    setMessage("公開しました。");
  }

  async function logout() {
    if (!confirmLeaveEditor()) return;
    await runEditorAction("logout", async () => {
      await adminApi.logout();
      setSignedIn(false);
      setPosts([]);
      setSelectedPost(null);
      setEditing(false);
      setTitle("");
      setMarkdown("");
      setOperation({ status: "idle" });
    });
  }

  async function unpublish() {
    if (!selectedPost || busy || uncertain) return;
    if (!window.confirm("この記事を非公開に戻しますか？")) return;
    await runEditorAction("unpublish", async () => {
      applySavedPost(await writePost("unpublish", selectedPost.id, () => adminApi.unpublish(selectedPost.id, selectedPost.version)));
      await reloadPostList();
      setMessage("非公開にしました。");
    });
  }

  function runWrite(action: "save" | "publish", execute: () => Promise<void>) {
    if (uncertain) return Promise.resolve();
    return runEditorAction(action, execute);
  }

  return {
    signedIn, password, setPassword, posts, selectedPost,
    title, setTitle, markdown, setMarkdown, busy, message, error, editing,
    mode, setMode, uncertain, dirty, confirmLeaveEditor, openPost, logout,
    login: () => runEditorAction("login", async () => {
      await adminApi.login(password);
      setPassword("");
      setSignedIn(true);
    }),
    saveDraft: () => runWrite("save", saveDraft),
    publish: () => runWrite("publish", publish),
    unpublish, checkResult,
  };
}
