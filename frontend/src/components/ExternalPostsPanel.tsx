import { useEffect, useRef, useState } from "react";
import {
  ApiError,
  outcomeIsUnknown,
  requestAdminJson,
} from "../features/editor/adminApi";
import { dateLabel, type ExternalPost, type SyncState } from "../lib/types";

const sourceNames = { qiita: "Qiita", note: "note", wantedly: "Wantedly" };
const statusNames: Record<string, string> = {
  waiting: "同期待ち",
  running: "同期中",
  success: "取得完了",
  partial: "取得範囲に制限あり",
  failed: "取得失敗",
};
const coverageNames: Record<string, string> = {
  all: "公開記事の全ページ",
  recent: "最新の取得可能な範囲",
  gap: "取得範囲に抜けがある可能性。補完が必要です。",
  unknown: "未取得",
};

export function ExternalPostsPanel({
  onLeaveStateChange,
  onSessionExpired,
  activeSession,
}: {
  onSessionExpired: () => void;
  activeSession: boolean;
  onLeaveStateChange: (state: { dirty: boolean; busy: boolean }) => void;
}) {
  const [posts, setPosts] = useState<ExternalPost[]>([]);
  const [states, setStates] = useState<SyncState[]>([]);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const inFlight = useRef(false);
  const [selected, setSelected] = useState<ExternalPost | null>(null);
  const [title, setTitle] = useState("");
  const [date, setDate] = useState("");
  const [hidden, setHidden] = useState(false);

  const dirty =
    selected !== null &&
    (title !== (selected.titleOverride ?? "") ||
      hidden !== selected.hidden ||
      date !== localDateInput(selected.dateOverride));
  useEffect(() => {
    onLeaveStateChange({ dirty: dirty || uncertain, busy });
  }, [dirty, uncertain, busy, onLeaveStateChange]);
  useEffect(
    () => () => onLeaveStateChange({ dirty: false, busy: false }),
    [onLeaveStateChange],
  );
  function confirmDiscard() {
    return (
      !busy &&
      ((!dirty && !uncertain) ||
        window.confirm(
          "未保存の補正または確認できていない保存結果を破棄しますか？",
        ))
    );
  }
  async function refresh() {
    const [articles, sync] = await Promise.all([
      requestAdminJson<ExternalPost[]>("/admin/external-posts"),
      requestAdminJson<SyncState[]>("/admin/sync"),
    ]);
    setPosts(articles);
    setStates(sync);
  }
  useEffect(() => {
    if (!activeSession) return;
    let active = true;
    Promise.all([
      requestAdminJson<ExternalPost[]>("/admin/external-posts"),
      requestAdminJson<SyncState[]>("/admin/sync"),
    ])
      .then(([articles, sync]) => {
        if (active) {
          setPosts(articles);
          setStates(sync);
        }
      })
      .catch((error: unknown) => {
        if (active) {
          if (error instanceof ApiError && error.status === 401)
            onSessionExpired();
          setError(
            "外部記事の状態を取得できませんでした。再取得してください。",
          );
        }
      });
    return () => {
      active = false;
    };
  }, [activeSession, onSessionExpired]);
  useEffect(() => {
    if (
      !activeSession ||
      !states.some(
        (state) => state.status === "waiting" || state.status === "running",
      )
    )
      return;
    const timer = window.setTimeout(() => {
      void refresh().catch(() =>
        setError("同期状態を確認できませんでした。再取得してください。"),
      );
    }, 5000);
    return () => window.clearTimeout(timer);
  }, [states, activeSession]);
  async function run(action: () => Promise<void>, writesCorrection = false) {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setError("");
    setMessage("");
    try {
      await action();
    } catch (error) {
      if (writesCorrection && outcomeIsUnknown(error)) setUncertain(true);
      if (error instanceof ApiError && error.status === 401) onSessionExpired();
      setError(
        error instanceof Error
          ? error.message
          : "状態を確認できませんでした。再取得してください。",
      );
    } finally {
      setBusy(false);
      inFlight.current = false;
    }
  }
  function select(post: ExternalPost) {
    if (!confirmDiscard()) return;
    setUncertain(false);
    setSelected(post);
    setTitle(post.titleOverride ?? "");
    setDate(localDateInput(post.dateOverride));
    setHidden(post.hidden);
  }
  return (
    <section className="management-panel" aria-labelledby="external-heading">
      <h2 id="external-heading">外部の記事</h2>
      <p className="muted">
        本文は取り込まず、元の記事へ案内します。取得できなかった記事や古い記事が含まれない場合があります。
      </p>
      <button
        className="secondary"
        disabled={busy}
        onClick={() => void run(refresh)}
      >
        外部記事の状態を再取得
      </button>
      {message && <p aria-live="polite">{message}</p>}
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <ul className="sync-sources">
        {states.map((state) => (
          <li key={state.source}>
            <strong>{sourceNames[state.source]}</strong> —{" "}
            {statusNames[state.status] ?? state.status}
            <p className="muted">
              {coverageNames[state.coverage] ?? "取得範囲を確認してください"}
              ・前回 {state.count}件
              {state.lastSuccess
                ? `・最終取得 ${dateLabel(state.lastSuccess)}`
                : ""}
            </p>
            {state.errorCode && (
              <p className="error">
                前回の取得に失敗しました（{state.errorCode}
                ）。既存の記事は保持しています。
              </p>
            )}
            <button
              disabled={busy || state.status === "running"}
              className="secondary"
              onClick={() =>
                void run(async () => {
                  await requestAdminJson(
                    `/admin/sync/${state.source}`,
                    "POST",
                    {},
                  );
                  await refresh();
                })
              }
            >
              {sourceNames[state.source]}を同期
            </button>
          </li>
        ))}
      </ul>
      <label>
        補正する外部記事
        <select
          value={selected ? `${selected.source}:${selected.id}` : ""}
          disabled={busy}
          onChange={(event) => {
            const post = posts.find(
              (post) => `${post.source}:${post.id}` === event.target.value,
            );
            if (post) select(post);
            else if (confirmDiscard()) {
              setSelected(null);
              setUncertain(false);
            }
          }}
        >
          <option value="">記事を選択</option>
          {posts.map((post) => (
            <option
              key={`${post.source}:${post.id}`}
              value={`${post.source}:${post.id}`}
            >
              {sourceNames[post.source]} — {post.titleOverride ?? post.title}
              {post.hidden ? "（非表示）" : ""}
            </option>
          ))}
        </select>
      </label>
      {uncertain && selected && (
        <div className="notice">
          <p>保存結果を確認できていません。自動再送はしていません。</p>
          <button
            className="secondary"
            disabled={busy}
            onClick={() =>
              void run(async () => {
                const latest = await requestAdminJson<ExternalPost[]>(
                  "/admin/external-posts",
                );
                const saved = latest.find(
                  (post) =>
                    post.source === selected.source && post.id === selected.id,
                );
                if (!saved)
                  throw new Error(
                    "保存先を特定できませんでした。状態を確認してください。",
                  );
                setPosts(latest);
                setSelected(saved);
                setUncertain(false);
              })
            }
          >
            外部記事の保存結果を確認
          </button>
          <p className="muted">入力は保持したまま、保存先の版を取得します。</p>
        </div>
      )}
      {selected && (
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void run(async () => {
              await requestAdminJson(
                `/admin/external-posts/${selected.source}/${encodeURIComponent(selected.id)}`,
                "PATCH",
                {
                  titleOverride: title.trim() || null,
                  dateOverride: date ? new Date(date).toISOString() : null,
                  hidden,
                  version: selected.version,
                },
              );
              setSelected(null);
              setUncertain(false);
              setMessage("外部記事の設定を保存しました。");
              await refresh();
            }, true);
          }}
        >
          <p>取得したタイトル：{selected.title}</p>
          <label>
            タイトルの補正（空欄で取得値を使用）
            <input
              value={title}
              maxLength={500}
              disabled={busy}
              onChange={(event) => setTitle(event.target.value)}
            />
          </label>
          <label>
            公開日時の補正（この端末の時刻、空欄で取得値を使用）
            <input
              type="datetime-local"
              value={date}
              disabled={busy}
              onChange={(event) => setDate(event.target.value)}
            />
          </label>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={hidden}
              disabled={busy}
              onChange={(event) => setHidden(event.target.checked)}
            />
            一覧に表示しない
          </label>
          <button disabled={busy || uncertain}>外部記事の設定を保存</button>
          <p className="muted">
            再同期しても補正・非表示は保持します。競合や応答不明の場合は、自動再送せず状態を再取得してください。
          </p>
        </form>
      )}
    </section>
  );
}

function localDateInput(value: string | null) {
  if (!value) return "";
  const date = new Date(value);
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000)
    .toISOString()
    .slice(0, 16);
}
