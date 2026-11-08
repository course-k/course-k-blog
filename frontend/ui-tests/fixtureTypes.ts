import type { AdminPost, SyncState } from "../src/lib/types";

export type FixtureConfig = {
  screen: "article" | "list" | "admin";
  /** Initial admin URL, e.g. "/admin?view=articles". */
  path?: string;
  scenario?: "success" | "signed-out";
  empty?: boolean;
  oidc?: boolean;
  posts?: Partial<AdminPost>[];
  /** Replaces the article body on the article screen. */
  articleMarkdown?: string;
  sync?: Partial<SyncState>[];
  /** Creation keys that already created an article: key → article ID and first payload. */
  creations?: Record<string, { postId: string; title: string; markdown: string }>;
};

/** A rule applied to the next matching request in the fixed API. */
export type FailureRule = {
  method: string;
  path: string;
  /** "lost": commit, then lose the response. "before": fail without committing.
   *  "status": answer with the status. "hold": wait until release(). */
  mode: "lost" | "before" | "status" | "hold";
  status?: number;
  body?: unknown;
  headers?: Record<string, string>;
  times?: number;
};

export type FixtureServer = {
  posts: Record<string, AdminPost>;
  calls: string[];
  bodies: { method: string; path: string; body: unknown }[];
  fail(rule: FailureRule): void;
  release(): void;
  signedIn: boolean;
  sync: SyncState[];
  imagesRegistered: number;
  /** File name → failure code returned by completion. */
  uploadFailures: Record<string, string>;
};

declare global {
  interface Window {
    fixture?: FixtureConfig;
    calls: string[];
    fixtureServer: FixtureServer;
  }
}
