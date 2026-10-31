import {
  useCallback,
  useEffect,
  useImperativeHandle,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ClipboardEvent,
  type DragEvent,
  type Ref,
} from "react";
import { Link } from "@tanstack/react-router";
import type { AdminPost } from "../lib/types";
import type { RecoveryRecord, RecoveryStore } from "../features/editor/draftRecovery";
import { useArticleEditor } from "../features/editor/useArticleEditor";
import { offsetFromPoint } from "../features/editor/dropPosition";
import { updateImageDisplay } from "../features/editor/imageMarkdown";
import { UnknownOutcomeError, type SessionSnapshot } from "../features/editor/articleSession";
import { Markdown } from "./Markdown";
import type { ImageContextValue } from "./BlogImage";

export type EditorHandle = {
  /** Saves (and waits for images) before leaving; may ask the owner. */
  prepareLeave(): Promise<boolean>;
  hasUnsavedWork(): boolean;
};

type LeaveQuestion = {
  kind: "images" | "save";
  unknown: boolean;
  resolve(choice: "stay" | "retry" | "leave"): void;
};

const saveLabels: Record<SessionSnapshot["save"], string> = {
  none: "",
  dirty: "未保存",
  saving: "保存中…",
  saved: "保存済み",
  retrying: "保存できませんでした。再試行します。",
  verifying: "保存結果を確認しています…",
  auth: "再ログインが必要です。入力は保持しています。",
  conflict: "保存先が別の画面で更新されています。",
  invalid: "保存できませんでした。",
  forbidden: "保存できませんでした。権限を確認してください。",
  missing: "記事が見つかりません。",
};

function statusText(snapshot: SessionSnapshot, images: { busy: boolean; failed: boolean }) {
  const parts: string[] = [];
  const save = saveLabels[snapshot.save];
  // Never show the whole article as saved while an image is unfinished.
  if (save && !(snapshot.save === "saved" && (images.busy || images.failed))) parts.push(save);
  if (snapshot.save === "invalid" && snapshot.message) parts.push(snapshot.message);
  if (images.busy) parts.push("画像を送信中…");
  if (images.failed) parts.push("画像の送信に失敗しました。");
  return parts.join(" ");
}

export function ArticleEditor({
  ref,
  articleId,
  restore,
  store,
  loginCount,
  onCreated,
  onAuthExpired,
  onStorageUnavailable,
}: {
  ref?: Ref<EditorHandle>;
  articleId: string | null;
  restore?: RecoveryRecord;
  store: RecoveryStore | null;
  loginCount: number;
  onCreated(post: AdminPost): void;
  onAuthExpired(): void;
  onStorageUnavailable(): void;
}) {
  const editor = useArticleEditor({ articleId, restore, store, onCreated, onAuthExpired, onStorageUnavailable });
  const { snapshot, session, imageState } = editor;
  const [mode, setMode] = useState<"write" | "preview">("write");
  const [selectedImage, setSelectedImage] = useState<number | null>(null);
  const [notice, setNotice] = useState("");
  const [operationError, setOperationError] = useState("");
  const [leave, setLeave] = useState<LeaveQuestion | null>(null);
  const textarea = useRef<HTMLTextAreaElement>(null);
  const lastValue = useRef(snapshot.doc.markdown);
  const selection = useRef({ start: 0, end: 0 });
  const pendingCaret = useRef<number | null>(null);
  const leaving = useRef<Promise<boolean> | null>(null);

  // Resume sending after the owner signs in again.
  const firstLogin = useRef(loginCount);
  useEffect(() => {
    if (loginCount === firstLogin.current) return;
    session?.resumeAfterLogin();
    editor.resumeUploadsAfterLogin();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loginCount, session]);

  // Keep the caret where the owner left it when an upload rewrites the text.
  useLayoutEffect(() => {
    const element = textarea.current;
    const next = snapshot.doc.markdown;
    const previous = lastValue.current;
    lastValue.current = next;
    if (!element || previous === next) return;
    if (pendingCaret.current !== null) {
      element.setSelectionRange(pendingCaret.current, pendingCaret.current);
      selection.current = { start: pendingCaret.current, end: pendingCaret.current };
      pendingCaret.current = null;
      return;
    }
    if (element.value !== next || document.activeElement !== element) return;
    let prefix = 0;
    while (prefix < previous.length && prefix < next.length && previous[prefix] === next[prefix]) prefix++;
    const shift = next.length - previous.length;
    const map = (offset: number) => (offset > prefix ? Math.max(prefix, offset + shift) : offset);
    if (element.selectionStart !== map(selection.current.start) && shift !== 0) {
      const { start, end } = selection.current;
      element.setSelectionRange(map(start), map(end));
      selection.current = { start: map(start), end: map(end) };
    }
  }, [snapshot.doc.markdown]);

  const rememberSelection = () => {
    const element = textarea.current;
    if (element) selection.current = { start: element.selectionStart, end: element.selectionEnd };
  };

  // Files dropped anywhere else must not navigate the tab to the file.
  useEffect(() => {
    const guard = (event: globalThis.DragEvent) => {
      if (!event.dataTransfer?.types.includes("Files")) return;
      if (event.target instanceof Node && textarea.current?.contains(event.target)) return;
      event.preventDefault();
      if (event.type === "drop") setNotice("画像は本文へドロップしてください。");
    };
    window.addEventListener("dragover", guard);
    window.addEventListener("drop", guard);
    return () => {
      window.removeEventListener("dragover", guard);
      window.removeEventListener("drop", guard);
    };
  }, []);

  // Clicking outside the selected image deselects it.
  useEffect(() => {
    if (selectedImage === null) return;
    const onDown = (event: PointerEvent) => {
      if (!(event.target instanceof Element) || !event.target.closest(".blog-image img, .image-selection"))
        setSelectedImage(null);
    };
    document.addEventListener("pointerdown", onDown);
    return () => document.removeEventListener("pointerdown", onDown);
  }, [selectedImage]);

  const ask = (kind: LeaveQuestion["kind"], unknown: boolean) =>
    new Promise<"stay" | "retry" | "leave">((resolve) => setLeave({ kind, unknown, resolve }));

  const prepareLeave = useCallback((): Promise<boolean> => {
    // A second navigation while one is pending does not run the save twice.
    leaving.current ??= (async () => {
      try {
        for (;;) {
          const result = await editor.prepareLeave();
          if (result === "saved") return true;
          const current = session?.getSnapshot();
          const unknown = current?.save === "verifying" || current?.save === "retrying";
          const choice = await ask(result === "images-failed" ? "images" : "save", unknown);
          setLeave(null);
          if (choice === "stay") return false;
          if (choice === "retry") {
            if (result === "images-failed") {
              // The live queue, not the uploads captured when leaving began.
              editor.retryFailedImages();
            } else {
              session?.retryNow();
            }
            continue;
          }
          if (result === "images-failed") {
            editor.discardFailedImages();
            continue; // save the remaining text before leaving
          }
          await editor.discardLocal();
          return true;
        }
      } finally {
        leaving.current = null;
      }
    })();
    return leaving.current;
  }, [editor, session]);

  useImperativeHandle(
    ref,
    () => ({
      prepareLeave,
      hasUnsavedWork: () => Boolean(session?.hasUnsavedWork()) || imageState.busy || imageState.failed,
    }),
    [prepareLeave, session, imageState],
  );

  const images = useMemo<ImageContextValue>(
    () => ({
      mode: "preview",
      editing: {
        selected: selectedImage,
        select: setSelectedImage,
        change: (start, display) => {
          const doc = session?.getSnapshot().doc;
          if (!doc) return;
          const markdown = updateImageDisplay(doc.markdown, start, display);
          if (markdown !== null) session!.setDocument({ ...doc, markdown });
        },
        pending: (id) => {
          const item = editor.uploads.find((upload) => upload.clientImageId === id);
          return item ? { status: item.status, error: item.error, previewUrl: editor.previews.get(id) } : undefined;
        },
        retry: editor.retryImage,
        remove: editor.removeImage,
      },
    }),
    [selectedImage, session, editor.uploads, editor.previews, editor.retryImage, editor.removeImage],
  );

  if (editor.load.status === "loading")
    return (
      <p className="notice" role="status">
        記事を読み込んでいます…
      </p>
    );
  if (editor.load.status === "missing" || editor.load.status === "error")
    return (
      <section className="editor-missing" role="alert">
        <h1>記事を開けませんでした</h1>
        <p>{editor.load.status === "missing" ? "指定された記事は見つかりません。" : editor.load.message}</p>
        <Link to="/admin" search={{ view: "articles" }}>
          記事一覧へ
        </Link>
      </section>
    );
  if (!session) return null;

  const doc = snapshot.doc;
  const setDoc = editor.setDocument;
  const blocked = ["auth", "conflict", "forbidden", "missing"].includes(snapshot.save);
  const hasChanges = snapshot.dirty || snapshot.serverHasUnpublishedChanges || !snapshot.published;
  const publishDisabled =
    snapshot.operation !== null ||
    imageState.busy ||
    imageState.failed ||
    blocked ||
    doc.title.trim() === "" ||
    (snapshot.published && !hasChanges);
  const publication = snapshot.published
    ? snapshot.dirty || snapshot.serverHasUnpublishedChanges
      ? "公開中・未公開の変更あり"
      : "公開中"
    : "下書き";

  async function runOperation(kind: "publish" | "unpublish") {
    setOperationError("");
    if (kind === "unpublish" && !window.confirm("この記事を非公開に戻しますか？")) return;
    try {
      await (kind === "publish" ? session!.publish() : session!.unpublish());
    } catch (error) {
      setOperationError(
        error instanceof UnknownOutcomeError || error instanceof Error ? error.message : "処理できませんでした。",
      );
    }
  }

  async function copyLocal() {
    try {
      await navigator.clipboard.writeText(`${doc.title}\n\n${doc.markdown}`);
      setNotice("この画面の内容をコピーしました。");
    } catch {
      textarea.current?.select();
      setNotice("コピーできませんでした。本文を選択したので手動でコピーしてください。");
    }
  }

  function onPaste(event: ClipboardEvent<HTMLTextAreaElement>) {
    // Clipboard files only: image data embedded in pasted HTML is ignored.
    const files = [...event.clipboardData.files];
    if (!files.some((file) => file.type.startsWith("image/"))) return;
    event.preventDefault();
    const element = event.currentTarget;
    const caret = editor.insertFiles(files, element.selectionStart, element.selectionEnd);
    if (caret !== null) pendingCaret.current = caret;
  }
  function onDragOver(event: DragEvent<HTMLTextAreaElement>) {
    if (!event.dataTransfer.types.includes("Files")) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
  }
  function onDrop(event: DragEvent<HTMLTextAreaElement>) {
    if (!event.dataTransfer.files.length) return;
    event.preventDefault();
    // Insert at the drop point, not at the old caret; selected text is not replaced.
    const position = offsetFromPoint(event.currentTarget, event.clientX, event.clientY);
    const caret = editor.insertFiles([...event.dataTransfer.files], position, position);
    if (caret !== null) {
      pendingCaret.current = caret;
      event.currentTarget.focus();
    }
  }

  return (
    <section className="article-editor" aria-labelledby="editor-heading">
      <header className="editor-bar">
        <div>
          <h1 id="editor-heading">{articleId || snapshot.id ? "記事を編集" : "新規作成"}</h1>
          <p className="editor-meta">
            <span className="publication-state">{publication}</span>
            <span className="save-state" role="status" aria-live="polite" data-state={snapshot.save}>
              {statusText(snapshot, imageState)}
            </span>
            {snapshot.save === "retrying" && (
              <button type="button" className="link-button" onClick={() => session.retryNow()}>
                再試行
              </button>
            )}
          </p>
        </div>
        <div className="publish-actions">
          <button type="button" disabled={publishDisabled} onClick={() => void runOperation("publish")}>
            {snapshot.operation === "publish"
              ? "公開しています…"
              : snapshot.published
                ? "更新を公開する"
                : "公開する"}
          </button>
          {snapshot.published && (
            <button
              type="button"
              className="secondary"
              disabled={snapshot.operation !== null || blocked}
              onClick={() => void runOperation("unpublish")}
            >
              非公開に戻す
            </button>
          )}
        </div>
      </header>
      {doc.title.trim() === "" && (doc.markdown !== "" || snapshot.id) && (
        <p className="muted">公開にはタイトルが必要です。</p>
      )}
      {(snapshot.operationMessage || operationError) && (
        <p className={operationError ? "error" : "muted"} role={operationError ? "alert" : "status"}>
          {operationError || snapshot.operationMessage}
        </p>
      )}
      {(notice || editor.rejections.length > 0) && (
        <div className="error" role="alert">
          {notice && <p>{notice}</p>}
          {editor.rejections.map((reason) => (
            <p key={reason}>{reason}</p>
          ))}
        </div>
      )}
      {snapshot.save === "conflict" && (
        <ConflictPanel
          snapshot={snapshot}
          onCopy={copyLocal}
          onOverwrite={() => {
            if (window.confirm("サーバー版を、この画面の内容で上書き保存しますか？")) session.overwriteWithLocal();
          }}
          onAdopt={() => {
            if (window.confirm("この画面の変更を破棄して、サーバー版を読み込みますか？")) session.adoptServer();
          }}
        />
      )}
      {editor.restoreConflict && (
        <section className="conflict-panel" role="alert">
          <h2>端末内の編集内容はサーバーより古い版が基になっています</h2>
          <p>
            自動では送信していません。サーバー版（版{editor.restoreConflict.server.version}）を表示しています。端末内の内容は版
            {editor.restoreConflict.basedOn}を基にしています。
          </p>
          <details>
            <summary>端末内の内容を表示</summary>
            <p>{editor.restoreConflict.local.title || "無題"}</p>
            <pre>{editor.restoreConflict.local.markdown}</pre>
          </details>
          <div className="actions">
            <button
              type="button"
              className="secondary"
              onClick={() =>
                void navigator.clipboard
                  ?.writeText(`${editor.restoreConflict!.local.title}\n\n${editor.restoreConflict!.local.markdown}`)
                  .then(() => setNotice("端末内の内容をコピーしました。"))
              }
            >
              端末内の内容をコピー
            </button>
            <button
              type="button"
              className="secondary"
              onClick={() => {
                if (window.confirm("サーバー版を端末内の内容で置き換えますか？")) editor.resolveRestoreConflict(true);
              }}
            >
              端末内の内容で置き換える
            </button>
            <button type="button" className="quiet" onClick={() => editor.resolveRestoreConflict(false)}>
              サーバー版のまま編集する
            </button>
          </div>
        </section>
      )}
      <label htmlFor="title">タイトル</label>
      <input
        id="title"
        maxLength={200}
        value={doc.title}
        onChange={(event) => setDoc({ ...doc, title: event.target.value })}
        onCompositionStart={() => session.setComposing(true)}
        onCompositionEnd={() => session.setComposing(false)}
      />
      <div className="editor-mode" role="group" aria-label="表示の切り替え">
        <button type="button" className="secondary" aria-pressed={mode === "write"} onClick={() => setMode("write")}>
          本文
        </button>
        <button type="button" className="secondary" aria-pressed={mode === "preview"} onClick={() => setMode("preview")}>
          プレビュー
        </button>
      </div>
      <div className="editor-columns" data-mode={mode}>
        <div className="markdown-input">
          <label htmlFor="markdown">本文（Markdown）</label>
          <textarea
            id="markdown"
            ref={textarea}
            value={doc.markdown}
            spellCheck={false}
            onChange={(event) => {
              rememberSelection();
              lastValue.current = event.target.value;
              setDoc({ ...doc, markdown: event.target.value });
            }}
            onSelect={rememberSelection}
            onKeyUp={rememberSelection}
            onCompositionStart={() => session.setComposing(true)}
            onCompositionEnd={() => session.setComposing(false)}
            onPaste={onPaste}
            onDragOver={onDragOver}
            onDrop={onDrop}
          />
        </div>
        <section
          className="preview"
          aria-label="プレビュー"
          onClick={(event) => {
            if (!(event.target as Element).closest(".blog-image img, .image-selection")) setSelectedImage(null);
          }}
        >
          <Markdown text={doc.markdown} images={images} />
        </section>
      </div>
      {leave && (
        <div className="leave-dialog" role="alertdialog" aria-modal="true" aria-labelledby="leave-title">
          <div>
            <h2 id="leave-title">
              {leave.kind === "images" ? "送信できていない画像があります" : "保存できていない変更があります"}
            </h2>
            <p>
              {leave.kind === "images"
                ? "画像の送信に失敗したため、まだ移動していません。"
                : "最新の内容を保存できなかったため、まだ移動していません。"}
              {leave.unknown && "サーバーでは保存済みの可能性があります。"}
            </p>
            <div className="actions">
              <button type="button" autoFocus onClick={() => leave.resolve("stay")}>
                編集を続ける
              </button>
              <button type="button" className="secondary" onClick={() => leave.resolve("retry")}>
                再試行
              </button>
              <button type="button" className="quiet" onClick={() => leave.resolve("leave")}>
                {leave.kind === "images" ? "未完了の画像を破棄して移動" : "変更を破棄して移動"}
              </button>
            </div>
          </div>
        </div>
      )}
    </section>
  );
}

function ConflictPanel({
  snapshot,
  onCopy,
  onOverwrite,
  onAdopt,
}: {
  snapshot: SessionSnapshot;
  onCopy(): void;
  onOverwrite(): void;
  onAdopt(): void;
}) {
  const server = snapshot.conflictServer;
  return (
    <section className="conflict-panel" role="alert">
      <h2>保存先が別の画面で更新されています</h2>
      <p>自動では上書きしていません。この画面の入力は保持しています。</p>
      {server && (
        <details>
          <summary>サーバー版を表示（版{server.version}）</summary>
          <p>タイトル：{server.title || "無題"}{server.title !== snapshot.doc.title && "（この画面と異なります）"}</p>
          <pre>{server.markdown}</pre>
        </details>
      )}
      <div className="actions">
        <button type="button" className="secondary" onClick={onCopy}>
          この画面の内容をコピー
        </button>
        {server && (
          <>
            <button type="button" className="secondary" onClick={onOverwrite}>
              この画面の内容で上書き保存
            </button>
            <button type="button" className="quiet" onClick={onAdopt}>
              サーバー版を読み込む
            </button>
          </>
        )}
      </div>
    </section>
  );
}
