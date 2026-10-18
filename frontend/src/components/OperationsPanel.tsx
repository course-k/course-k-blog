import { useEffect, useState } from "react";
import { ApiError, requestAdminJson } from "../features/editor/adminApi";

type Operations = {
  state: {
    generation: number;
    backedUpGeneration: number;
    running: boolean;
    lastSuccess: string | null;
    lastError: string;
    dueAt: string | null;
  };
  backups: { id: string; createdAt: string; size: number }[];
};
export function OperationsPanel({
  onSessionExpired,
  activeSession,
}: {
  onSessionExpired: () => void;
  activeSession: boolean;
}) {
  const [data, setData] = useState<Operations | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function refresh() {
    try {
      setData(await requestAdminJson<Operations>("/admin/operations"));
      setError("");
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) onSessionExpired();
      setError(
        "バックアップの状態を取得できませんでした。再取得してください。",
      );
    }
  }
  useEffect(() => {
    if (!activeSession) return;
    let active = true;
    const read = () =>
      requestAdminJson<Operations>("/admin/operations")
        .then((data) => {
          if (active) {
            setData(data);
            setError("");
          }
        })
        .catch((error: unknown) => {
          if (active) {
            if (error instanceof ApiError && error.status === 401)
              onSessionExpired();
            setError(
              "バックアップの状態を取得できませんでした。再取得してください。",
            );
          }
        });
    void read();
    const timer = window.setInterval(() => void read(), 15000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [activeSession, onSessionExpired]);
  return (
    <section className="management-panel" aria-labelledby="operations-heading">
      <h2 id="operations-heading">バックアップ</h2>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {data && (
        <>
          <p aria-live="polite">
            {data.state.running
              ? "作成中です。"
              : data.state.lastError
                ? "前回の処理に失敗しました。状態と保存先を確認してください。"
                : data.state.generation > data.state.backedUpGeneration
                  ? "バックアップ待ちの変更があります。"
                  : data.state.lastSuccess
                    ? "直近の変更はバックアップ済みです。"
                    : "バックアップが必要な変更はありません。"}
          </p>
          <p className="muted">
            {data.state.lastSuccess
              ? `最終成功（日本時間）：${new Date(data.state.lastSuccess).toLocaleString("ja-JP", { timeZone: "Asia/Tokyo" })}`
              : "まだバックアップはありません。"}{" "}
            記事の公開成功とバックアップ成功は別々に確認します。
          </p>
          <button
            className="secondary"
            disabled={
              busy ||
              data.state.running ||
              data.state.generation <= data.state.backedUpGeneration
            }
            onClick={async () => {
              setBusy(true);
              try {
                await requestAdminJson("/admin/backups", "POST", {});
                await refresh();
              } catch (error) {
                if (error instanceof ApiError && error.status === 401)
                  onSessionExpired();
                setError(
                  "バックアップの要求を確認できませんでした。状態を再取得してください。",
                );
              } finally {
                setBusy(false);
              }
            }}
          >
            変更を今バックアップする
          </button>
          <details>
            <summary>保存済みのバックアップ</summary>
            <ul>
              {data.backups.map((record) => (
                <li key={record.id}>
                  {new Date(record.createdAt).toLocaleString("ja-JP", {
                    timeZone: "Asia/Tokyo",
                  })}
                  ・復元用ID：<code>{record.id}</code>
                </li>
              ))}
            </ul>
          </details>
        </>
      )}
      <button
        className="secondary"
        disabled={busy}
        onClick={() => void refresh()}
      >
        バックアップの状態を再取得
      </button>
    </section>
  );
}
