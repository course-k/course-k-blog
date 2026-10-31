import { useCallback, useEffect, useRef, useState } from "react";
import { adminApi, ApiError, requestAdminJson } from "./adminApi";

type AuthMode = "loading" | "password" | "oidc" | "unavailable";

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
    adminApi
      .session()
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
      });
    return () => {
      active = false;
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

  /** Explicit logout: ends the session first, then the identity provider's. */
  async function logout(): Promise<boolean> {
    try {
      await adminApi.logout();
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) requestLogin();
      return false;
    }
    setSignedIn(false);
    setHasWorkspace(false);
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
    loginCount,
    leavingForLogout,
    requestLogin,
    login,
    loginWithProvider,
    cancelProviderLogin: () => providerWait.current?.abort(),
    logout,
  };
}
