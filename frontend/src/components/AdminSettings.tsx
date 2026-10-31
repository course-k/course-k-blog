import { useCallback, useEffect, useRef, useState } from "react";
import { adminApi, ApiError, requestAdminJson } from "../features/editor/adminApi";
import type { SyncState } from "../lib/types";

const sources: { source: SyncState["source"]; name: string }[] = [
  { source: "qiita", name: "Qiita" },
  { source: "wantedly", name: "Wantedly" },
  { source: "note", name: "note" },
];

const reasons: Record<string, string> = {
  upstream_unavailable: "媒体に接続できませんでした",
  upstream_429: "媒体側の利用制限に達しました",
  robots_disallowed: "媒体の取得ルールで許可されていません",
  format_changed: "媒体の応答形式が変わりました",
  owner_mismatch: "本人の記事として確認できませんでした",
  response_too_large_or_incomplete: "応答が大きすぎるか途中で切れました",
};
const reason = (code: string) =>
  reasons[code] ?? (/^upstream_\d+$/.test(code) ? `媒体がエラーを返しました（${code.slice(9)}）` : code || "理由不明");

const tokyo = (value: string) =>
  new Date(value).toLocaleString("ja-JP", {
    timeZone: "Asia/Tokyo",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });

export function AdminSettings({
  onAuthExpired,
  onLogout,
}: {
  onAuthExpired(): void;
  onLogout(): Promise<boolean>;
}) {
  const [logoutError, setLogoutError] = useState("");
  const [loggingOut, setLoggingOut] = useState(false);
  return (
    <section className="admin-settings" aria-labelledby="settings-heading">
      <h1 id="settings-heading">設定</h1>
      <SyncSettings onAuthExpired={onAuthExpired} />
      <BackupSettings onAuthExpired={onAuthExpired} />
      <section className="settings-section" aria-labelledby="logout-heading">
        <h2 id="logout-heading">ログアウト</h2>
        <button
          type="button"
          className="secondary"
          disabled={loggingOut}
          onClick={async () => {
            setLoggingOut(true);
            setLogoutError("");
            const ok = await onLogout();
            if (!ok) setLogoutError("ログアウトできませんでした。時間をおいて再試行してください。");
            setLoggingOut(false);
          }}
        >
          {loggingOut ? "ログアウトしています…" : "ログアウト"}
        </button>
        {logoutError && (
          <p className="error" role="alert">
            {logoutError}
          </p>
        )}
      </section>
    </section>
  );
}

function SyncSettings({ onAuthExpired }: { onAuthExpired(): void }) {
  const [states, setStates] = useState<SyncState[] | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [notes, setNotes] = useState<Partial<Record<SyncState["source"], string>>>({});
  const [requesting, setRequesting] = useState<Set<SyncState["source"]>>(new Set());
  const mounted = useRef(true);

  const refresh = useCallback(async () => {
    try {
      const next = await adminApi.syncStates();
      if (!mounted.current) return;
      setStates(next);
      setLoadFailed(false);
    } catch (error) {
      if (!mounted.current) return;
      if (error instanceof ApiError && error.status === 401) onAuthExpired();
      setLoadFailed(true);
    }
  }, [onAuthExpired]);

  useEffect(() => {
    mounted.current = true;
    void refresh();
    return () => {
      mounted.current = false;
    };
  }, [refresh]);

  // Poll every 3 s only while a request waits or runs; stop when leaving settings.
  const active = states?.some((state) => state.status === "waiting" || state.status === "running");
  useEffect(() => {
    if (!active) return;
    const timer = window.setInterval(() => void refresh(), 3000);
    return () => window.clearInterval(timer);
  }, [active, refresh]);

  async function request(source: SyncState["source"], name: string) {
    setRequesting((current) => new Set(current).add(source));
    setNotes((current) => ({ ...current, [source]: undefined }));
    try {
      await adminApi.requestSync(source);
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) onAuthExpired();
      const body = error instanceof ApiError ? (error.body as { reason?: string; retryAfterSeconds?: number } | null) : null;
      const note =
        body?.reason === "cooldown"
          ? `${name}は前回の同期から10分経つまで再実行できません（あと${Math.ceil((body.retryAfterSeconds ?? 60) / 60)}分）。`
          : body?.reason === "running"
            ? `${name}は同期の待機中または実行中です。`
            : `${name}の同期を要求できませんでした。`;
      setNotes((current) => ({ ...current, [source]: note }));
    } finally {
      setRequesting((current) => {
        const next = new Set(current);
        next.delete(source);
        return next;
      });
      await refresh();
    }
  }

  return (
    <section className="settings-section" aria-labelledby="sync-heading">
      <h2 id="sync-heading">外部記事の同期</h2>
      <div className="sync-icons" role="group" aria-label="媒体ごとの同期">
        {sources.map(({ source, name }) => {
          const state = states?.find((item) => item.source === source);
          const running = state?.status === "waiting" || state?.status === "running";
          const busy = running || requesting.has(source);
          return (
            <button
              key={source}
              type="button"
              className="sync-icon"
              aria-label={`${name}を同期`}
              title={`${name}を同期`}
              aria-busy={busy || undefined}
              disabled={busy || states === null}
              data-state={state?.status}
              onClick={() => void request(source, name)}
            >
              <img src={`/brand/sources/${source}.png`} width="20" height="20" alt="" />
            </button>
          );
        })}
      </div>
      <ul className="sync-results" aria-live="polite">
        {sources.map(({ source, name }) => {
          const state = states?.find((item) => item.source === source);
          const text = notes[source] ?? describe(state, name);
          if (!text) return null;
          return (
            <li key={source} data-source={source}>
              {text}
              {state?.status === "failed" && !notes[source] && (
                <button type="button" className="link-button" onClick={() => void request(source, name)}>
                  {name}を再試行
                </button>
              )}
            </li>
          );
        })}
      </ul>
      {loadFailed && (
        <p className="error" role="alert">
          同期状態を取得できませんでした。{" "}
          <button type="button" className="link-button" onClick={() => void refresh()}>
            状態を再確認
          </button>
        </p>
      )}
    </section>
  );
}

function describe(state: SyncState | undefined, name: string) {
  if (!state) return "";
  const when = state.lastSuccess ? `（${tokyo(state.lastSuccess)}）` : "";
  // count is the latest batch, not the total number of articles.
  const batch = `今回の取得：${state.count}件`;
  switch (state.status) {
    case "waiting":
      return `${name}の同期を待っています…`;
    case "running":
      return `${name}を同期しています…`;
    case "success":
      return `${name}の同期が完了しました${when}。${batch}。`;
    case "partial":
      return `${name}の取得可能な範囲を同期しました${when}。${batch}。${
        state.coverage === "gap" ? "取得できていない記事がある可能性があります。" : ""
      }`;
    case "failed":
      return `${name}の同期に失敗しました：${reason(state.errorCode)}。取得済みの記事はそのまま表示しています。`;
    default:
      return "";
  }
}

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

function BackupSettings({ onAuthExpired }: { onAuthExpired(): void }) {
  const [data, setData] = useState<Operations | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [requestError, setRequestError] = useState("");
  const [busy, setBusy] = useState(false);
  const read = useCallback(async () => {
    try {
      const next = await requestAdminJson<Operations>("/admin/operations");
      setData(next);
      setLoadFailed(false);
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) onAuthExpired();
      setLoadFailed(true);
    }
  }, [onAuthExpired]);
  // Same 15 s cycle as before, only while settings are open.
  useEffect(() => {
    void read();
    const timer = window.setInterval(() => void read(), 15000);
    return () => window.clearInterval(timer);
  }, [read]);
  const state = data?.state;
  const pending = state ? state.generation > state.backedUpGeneration : false;
  const summary = !state
    ? ""
    : state.running
      ? "実行中です。"
      : state.lastError
        ? "前回のバックアップに失敗しました。保存先と運用状態を確認してください。"
        : pending
          ? "変更待ちです。夜間または公開時にバックアップします。"
          : "正常です。直近の変更はバックアップ済みです。";
  return (
    <section className="settings-section" aria-labelledby="backup-heading">
      <h2 id="backup-heading">バックアップ</h2>
      {state && (
        <>
          <p aria-live="polite" data-state={state.running ? "running" : state.lastError ? "failed" : pending ? "pending" : "ok"}>
            {summary}
          </p>
          <p className="muted">
            {state.lastSuccess ? `最終成功（日本時間）：${tokyo(state.lastSuccess)}` : "まだバックアップはありません。"}
          </p>
          <button
            type="button"
            className="secondary"
            disabled={busy || state.running || !pending}
            onClick={async () => {
              setBusy(true);
              setRequestError("");
              try {
                await requestAdminJson("/admin/backups", "POST", {});
              } catch (error) {
                if (error instanceof ApiError && error.status === 401) onAuthExpired();
                setRequestError("バックアップを完了できたか確認できませんでした。状態を確認してください。");
              } finally {
                setBusy(false);
                await read();
              }
            }}
          >
            {busy ? "バックアップしています…" : "今すぐバックアップ"}
          </button>
          {!pending && !state.running && <p className="muted">バックアップが必要な変更はありません。</p>}
          <details>
            <summary>保存履歴</summary>
            {data.backups.length === 0 ? (
              <p className="muted">保存済みのバックアップはありません。</p>
            ) : (
              <ul>
                {data.backups.map((record) => (
                  <li key={record.id}>
                    {tokyo(record.createdAt)}・復元用ID：<code>{record.id}</code>
                  </li>
                ))}
              </ul>
            )}
          </details>
        </>
      )}
      {requestError && (
        <p className="error" role="alert">
          {requestError}
        </p>
      )}
      {loadFailed && (
        <p className="error" role="alert">
          バックアップの状態を取得できませんでした。{" "}
          <button type="button" className="link-button" onClick={() => void read()}>
            状態を再確認
          </button>
        </p>
      )}
    </section>
  );
}
