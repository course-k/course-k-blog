import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import type { AdminPost } from "../../lib/types";
import { adminApi, ApiError } from "./adminApi";
import { ArticleSession, type Doc, type SessionSnapshot } from "./articleSession";
import {
  newLocalId,
  RECOVERY_SCHEMA,
  RecoveryWriter,
  tabSessionId,
  type RecoveryRecord,
  type RecoveryStore,
} from "./draftRecovery";
import { ImageUploadQueue, browserUploadApi, rejectImage, type UploadItem } from "./imageUploads";
import { insertImageSlots, pendingIds, removePending, resolvePending } from "./imageMarkdown";

export type LoadState =
  | { status: "loading" }
  | { status: "ready" }
  | { status: "missing" }
  | { status: "error"; message: string };

export type RestoreConflict = { local: Doc; basedOn: number; server: AdminPost };

type Options = {
  articleId: string | null;
  restore?: RecoveryRecord;
  store: RecoveryStore | null;
  onCreated(post: AdminPost): void;
  onAuthExpired(): void;
  onStorageUnavailable(): void;
};

const sessionApi = {
  create: adminApi.create,
  save: adminApi.save,
  get: adminApi.get,
  findCreation: adminApi.findCreation,
  publish: adminApi.publish,
  unpublish: adminApi.unpublish,
};

const emptySnapshot: SessionSnapshot = {
  id: null,
  doc: { title: "", markdown: "" },
  version: null,
  published: false,
  publishedAt: null,
  serverHasUnpublishedChanges: false,
  save: "none",
  message: "",
  operation: null,
  operationMessage: "",
  conflictServer: null,
  retryAt: null,
  dirty: false,
};

/** One editing session: DB autosave, image uploads and the device-local copy. */
export function useArticleEditor(options: Options) {
  const optionsRef = useRef(options);
  optionsRef.current = options;
  const [load, setLoad] = useState<LoadState>(
    options.articleId ? { status: "loading" } : { status: "ready" },
  );
  const [session, setSession] = useState<ArticleSession | null>(null);
  const [uploads, setUploads] = useState<UploadItem[]>([]);
  const [restoreConflict, setRestoreConflict] = useState<RestoreConflict | null>(null);
  const [rejections, setRejections] = useState<string[]>([]);
  const previews = useRef(new Map<string, string>());
  const localId = useRef(options.restore?.localId ?? newLocalId());
  const recordKey = `${tabSessionId}:${localId.current}`;
  const writer = useRef<RecoveryWriter | null>(null);
  const queueRef = useRef<ImageUploadQueue | null>(null);
  const sessionRef = useRef<ArticleSession | null>(null);

  // Build the session once per editor mount.
  useEffect(() => {
    let active = true;
    const { articleId, restore, store } = optionsRef.current;
    if (store)
      writer.current = new RecoveryWriter(store, () => optionsRef.current.onStorageUnavailable());
    const hooks = {
      beforeCreate: async () => {
        // The first payload and key reach the device before the request is sent.
        if (writer.current) {
          writer.current.schedule(buildRecord);
          await writer.current.writeNow();
        }
      },
      onCreated: (post: AdminPost) => optionsRef.current.onCreated(post),
      onAuthExpired: () => optionsRef.current.onAuthExpired(),
    };
    const queue = new ImageUploadQueue(browserUploadApi(adminApi), {
      onChange: () => {
        if (!active) return;
        setUploads(queue.list().map((item) => ({ ...item })));
        persist();
      },
      onAuthExpired: () => {
        if (active) optionsRef.current.onAuthExpired();
      },
      onDone: (item, result) => {
        if (!active) return;
        const current = sessionRef.current;
        if (current) {
          const doc = current.getSnapshot().doc;
          current.setDocument({ ...doc, markdown: resolvePending(doc.markdown, item.clientImageId, result.url) });
        }
        revoke(item.clientImageId);
      },
    });
    queueRef.current = queue;
    const start = (created: ArticleSession) => {
      if (!active) {
        created.dispose();
        return;
      }
      sessionRef.current = created;
      setSession(created);
      if (restore) {
        // The restored copy now belongs to this tab under a new key.
        void store?.delete(restore.key).catch(() => {});
        const images = restore.pendingImages.filter((image) =>
          pendingIds(created.getSnapshot().doc.markdown).includes(image.clientImageId),
        );
        for (const image of images) previews.current.set(image.clientImageId, URL.createObjectURL(image.blob));
        queue.add(images);
      }
      setLoad({ status: "ready" });
    };
    if (articleId) {
      adminApi
        .get(articleId)
        .then((post) => {
          if (!active) return;
          if (restore && restore.base && restore.base.version !== post.version) {
            setRestoreConflict({ local: restore.doc, basedOn: restore.base.version, server: post });
            start(new ArticleSession(sessionApi, { post }, hooks));
            return;
          }
          start(new ArticleSession(sessionApi, { post, doc: restore?.doc }, hooks));
        })
        .catch((error: unknown) => {
          if (!active) return;
          if (error instanceof ApiError && error.status === 404) setLoad({ status: "missing" });
          else if (error instanceof ApiError && error.status === 401) {
            optionsRef.current.onAuthExpired();
            setLoad({ status: "error", message: "再ログインしてから開き直してください。" });
          } else setLoad({ status: "error", message: "記事を読み込めませんでした。" });
        });
    } else {
      start(
        new ArticleSession(
          sessionApi,
          restore
            ? { doc: restore.doc, creationKey: restore.creationKey, initialPayload: restore.initialPayload }
            : {},
          hooks,
        ),
      );
    }
    return () => {
      active = false;
      queue.dispose();
      sessionRef.current?.dispose();
      for (const url of previews.current.values()) URL.revokeObjectURL(url);
      previews.current.clear();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function revoke(clientImageId: string) {
    const url = previews.current.get(clientImageId);
    if (url) URL.revokeObjectURL(url);
    previews.current.delete(clientImageId);
  }

  function buildRecord(): RecoveryRecord | null {
    const current = sessionRef.current;
    const queue = queueRef.current;
    if (!current) return null;
    const state = current.recoveryState();
    const pending = (queue?.list() ?? []).filter((item) => item.status !== "done");
    if (state.clean && pending.length === 0) return null;
    if (!state.articleId && !state.initialPayload && state.doc.title === "" && state.doc.markdown === "")
      return null;
    return {
      schema: RECOVERY_SCHEMA,
      key: recordKey,
      tabSessionId,
      localId: localId.current,
      articleId: state.articleId,
      creationKey: state.creationKey,
      initialPayload: state.initialPayload,
      base: state.base,
      doc: state.doc,
      pendingImages: pending.map(({ clientImageId, requestId, name, type, blob }) => ({
        clientImageId,
        requestId,
        name,
        type,
        blob,
      })),
      updatedAt: Date.now(),
    };
  }

  function persist() {
    const target = writer.current;
    if (!target) return;
    if (!buildRecord()) void target.remove(recordKey);
    else target.schedule(buildRecord);
  }

  const snapshot = useSyncExternalStore(
    session?.subscribe ?? noopSubscribe,
    session?.getSnapshot ?? (() => emptySnapshot),
    () => emptySnapshot,
  );

  // Keep the device copy current and cancel uploads whose slot was deleted.
  useEffect(() => {
    if (!session) return;
    persist();
    const queue = queueRef.current;
    if (!queue) return;
    const present = new Set(pendingIds(snapshot.doc.markdown));
    for (const item of queue.list())
      if (item.status !== "done" && !present.has(item.clientImageId)) {
        queue.cancel(item.clientImageId);
        revoke(item.clientImageId);
      }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [snapshot, session]);

  useEffect(() => {
    const onHidden = () => {
      if (document.visibilityState === "hidden") void writer.current?.writeNow();
    };
    const onOnline = () => sessionRef.current?.retryNow();
    document.addEventListener("visibilitychange", onHidden);
    window.addEventListener("online", onOnline);
    return () => {
      document.removeEventListener("visibilitychange", onHidden);
      window.removeEventListener("online", onOnline);
    };
  }, []);

  const setDocument = useCallback((doc: Doc) => sessionRef.current?.setDocument(doc), []);

  /** Pasted or dropped files: one slot per accepted image, in the given order. */
  const insertFiles = useCallback((files: File[], from: number, to: number) => {
    const current = sessionRef.current;
    const queue = queueRef.current;
    if (!current || !queue) return null;
    const accepted: File[] = [];
    const rejected: string[] = [];
    for (const file of files) {
      const reason = rejectImage(file);
      if (reason) rejected.push(reason);
      else accepted.push(file);
    }
    setRejections(rejected);
    if (!accepted.length) return null;
    const slots = accepted.map((file) => ({ file, clientImageId: crypto.randomUUID() }));
    const doc = current.getSnapshot().doc;
    const inserted = insertImageSlots(
      doc.markdown,
      from,
      to,
      slots.map(({ file, clientImageId }) => ({ clientImageId, alt: file.name.replace(/\.[^.]+$/, "") || file.name })),
    );
    for (const { file, clientImageId } of slots) previews.current.set(clientImageId, URL.createObjectURL(file));
    current.setDocument({ ...doc, markdown: inserted.markdown });
    queue.add(slots.map(({ file, clientImageId }) => ({ clientImageId, name: file.name, type: file.type, blob: file })));
    return inserted.caret;
  }, []);

  const removeImage = useCallback((clientImageId: string) => {
    const current = sessionRef.current;
    if (!current) return;
    const doc = current.getSnapshot().doc;
    current.setDocument({ ...doc, markdown: removePending(doc.markdown, clientImageId) });
  }, []);

  /** Resolves when it is safe to leave: images sent and the latest text saved. */
  const prepareLeave = useCallback(async (): Promise<"saved" | "images-failed" | "save-failed"> => {
    const queue = queueRef.current;
    const current = sessionRef.current;
    if (!current) return "saved";
    while (queue?.busy()) await new Promise((resolve) => setTimeout(resolve, 200));
    if (queue?.failed()) return "images-failed";
    return (await current.flush()) ? "saved" : "save-failed";
  }, []);

  const discardFailedImages = useCallback(() => {
    const queue = queueRef.current;
    const current = sessionRef.current;
    if (!queue || !current) return;
    let markdown = current.getSnapshot().doc.markdown;
    for (const item of queue.list())
      if (item.status === "failed") {
        markdown = removePending(markdown, item.clientImageId);
        queue.cancel(item.clientImageId);
        revoke(item.clientImageId);
      }
    current.setDocument({ ...current.getSnapshot().doc, markdown });
  }, []);

  /** Explicit discard: only unsaved local changes; the DB article stays. */
  const discardLocal = useCallback(async () => {
    sessionRef.current?.dispose();
    await writer.current?.remove(recordKey);
  }, [recordKey]);

  const imageState = useMemo(
    () => ({
      busy: uploads.some((item) => item.status === "queued" || item.status === "uploading"),
      failed: uploads.some((item) => item.status === "failed"),
    }),
    [uploads],
  );

  return {
    load,
    session,
    snapshot,
    uploads,
    previews: previews.current,
    imageState,
    rejections,
    restoreConflict,
    resolveRestoreConflict: (useLocal: boolean) => {
      if (useLocal && restoreConflict) sessionRef.current?.setDocument(restoreConflict.local);
      setRestoreConflict(null);
    },
    setDocument,
    insertFiles,
    removeImage,
    retryImage: (id: string) => queueRef.current?.retry(id),
    retryFailedImages: () => queueRef.current?.retryFailed(),
    resumeUploadsAfterLogin: () => queueRef.current?.resumeAfterLogin(),
    prepareLeave,
    discardFailedImages,
    discardLocal,
    flushRecovery: () => writer.current?.writeNow(),
  };
}

const noopSubscribe = () => () => {};
