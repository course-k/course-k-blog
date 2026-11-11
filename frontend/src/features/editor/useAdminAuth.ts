import { useCallback, useEffect, useRef, useState } from "react";
import { adminApi, ApiError, requestAdminJson } from "./adminApi";

type AuthMode = "loading" | "password" | "oidc" | "unavailable";

// Where the database pauses when idle, the first request waits for it to
// resume: about 15 seconds, 30 or more after a long pause.
const FIRST_ANSWER_TIMEOUT_MS = 60_000;
const SLOW_START_NOTICE_MS = 3_000;

// Owner sign-in only. Editing state lives in the article session, so an
// expired session hides the workspace without discarding it.
export function useAdminAuth() {
  const [signedIn, setSignedIn] = useState<boolean | null>(null);
  // Keep the workspace mounted across expiry; drop it only after explicit logout.
  const [hasWorkspace, setHasWorkspace] = useState(false);
  const [authMode, setAuthMode] = useState<AuthMode>("loading");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // True while the first session check is taking longer than a normal answer.
  const [slowStart, setSlowStart] = useState(false);
  const providerWait = useRef<AbortController | null>(null);
  const leavingForLogout = useRef(false);
  const [loginCount, setLoginCount] = useState(0);
  const requestLogin = useCallback(() => setSignedIn(false), []);

  useEffect(() => {
    let active = true;
    requestAdminJson<{ mode: string }>("/auth/config")
      .then((config) => {
        if (active)
          setAuthMode(config.mode === "oidc" || config.mode === "password" ? config.mode : "unavailable");
      })
      .catch(() => {
        if (active) setAuthMode("unavailable");
      });
    const firstAnswer = new AbortController();
    const deadline = setTimeout(() => firstAnswer.abort(new Error("first answer timed out")), FIRST_ANSWER_TIMEOUT_MS);
    const notice = setTimeout(() => active && setSlowStart(true), SLOW_START_NOTICE_MS);
    adminApi
      .session(firstAnswer.signal)
      .then(() => {
        if (active) {
          setSignedIn(true);
          setHasWorkspace(true);
        }
      })
      .catch((error: unknown) => {
        if (!active) return;
        setSignedIn(false);
        if (!(error instanceof ApiError && error.status === 401))
          setError("接続を確認できませんでした。再読み込みしてください。");
      })
      .finally(() => {
        clearTimeout(deadline);
        clearTimeout(notice);
        if (active) setSlowStart(false);
      });
    return () => {
      active = false;
      clearTimeout(deadline);
      clearTimeout(notice);
      providerWait.current?.abort();
    };
  }, []);

  const signedInNow = () => {
    setSignedIn(true);
    setHasWorkspace(true);
    setLoginCount((count) => count + 1);
  };

  async function login() {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await adminApi.login(password);
      setPassword("");
      signedInNow();
    } catch (error) {
      setError(error instanceof Error ? error.message : "ログインできませんでした。");
    } finally {
      setBusy(false);
    }
  }

  async function loginWithProvider() {
    if (busy) return;
    setBusy(true);
    setError("");
    const popup = window.open("/api/auth/login", "coursek-login", "popup,width=600,height=760");
    if (!popup) {
      setBusy(false);
      setError("ログイン画面を開けませんでした。このサイトのポップアップを許可して再試行してください。");
      return;
    }
    const controller = new AbortController();
    providerWait.current = controller;
    const deadline = Date.now() + 5 * 60_000;
    try {
      while (Date.now() < deadline && !controller.signal.aborted) {
        try {
          await adminApi.session(AbortSignal.any([controller.signal, AbortSignal.timeout(15_000)]));
          signedInNow();
          return;
        } catch (error) {
          if (controller.signal.aborted) break;
          if (!(error instanceof ApiError && error.status === 401)) throw error;
        }
        await new Promise((resolve) => window.setTimeout(resolve, 1500));
      }
      setError("ログインの確認を終了しました。入力は保持しています。必要なら再試行してください。");
    } catch (error) {
      setError(error instanceof Error ? error.message : "ログインを確認できませんでした。");
    } finally {
      try {
        popup.close();
      } catch {
        /* The owner can close the separate window. */
      }
      providerWait.current = null;
      setBusy(false);
    }
  }

  /** Explicit logout: ends the session, completes local cleanup (device
   * recovery copies), then leaves for the identity provider's logout. */
  async function logout(afterSignedOut?: () => Promise<boolean>): Promise<boolean> {
    try {
      await adminApi.logout();
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) requestLogin();
      return false;
    }
    const cleaned = afterSignedOut ? await afterSignedOut() : true;
    setSignedIn(false);
    setHasWorkspace(false);
    if (!cleaned) {
      // Stay here so the owner sees it; the server session is already ended.
      setError("ログアウトしました。この端末の一時保存を削除できませんでした。ブラウザのサイトデータを削除してください。");
      return true;
    }
    if (authMode === "oidc") {
      leavingForLogout.current = true;
      window.location.assign("/api/auth/logout");
    }
    return true;
  }

  return {
    signedIn,
    hasWorkspace,
    authMode,
    password,
    setPassword,
    busy,
    error,
    slowStart,
    loginCount,
    leavingForLogout,
    requestLogin,
    login,
    loginWithProvider,
    cancelProviderLogin: () => providerWait.current?.abort(),
    logout,
  };
}
