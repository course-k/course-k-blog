export type PublicPost = {
  id: string;
  title: string;
  markdown: string;
  publishedAt: string;
  updatedAt: string;
};

// Includes working copies of both published and unpublished articles.
export type AdminPost = {
  id: string;
  title: string;
  markdown: string;
  version: number;
  published: boolean;
  publishedAt: string | null;
  updatedAt: string;
  hasUnpublishedChanges: boolean;
};

export function dateLabel(value: string) {
  return new Intl.DateTimeFormat("ja-JP", {
    timeZone: "Asia/Tokyo",
    year: "numeric",
    month: "long",
    day: "numeric",
  }).format(new Date(value));
}

export type PublicPostSummary = Omit<PublicPost, "markdown">;

export type ArticleSummary =
  | ({ kind: "own" } & PublicPostSummary)
  | {
      kind: "external";
      id: string;
      title: string;
      publishedAt: string;
      source: "qiita" | "note" | "wantedly";
      url: string;
    };
export type ArticlePage = { articles: ArticleSummary[]; hasNext: boolean };
export type ExternalPost = {
  source: "qiita" | "note" | "wantedly";
  id: string;
  title: string;
  url: string;
  publishedAt: string;
  titleOverride: string | null;
  dateOverride: string | null;
  hidden: boolean;
  version: number;
};
export type SyncState = {
  source: ExternalPost["source"];
  status: string;
  coverage: string;
  count: number;
  errorCode: string;
  lastAttempt: string | null;
  lastSuccess: string | null;
  nextRun: string;
};
