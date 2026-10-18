export type FixtureConfig = {
  screen: "article" | "list" | "admin";
  scenario?: "success" | "publish-failure" | "list-failure" | "lost-response" | "conflict" | "signed-out";
  empty?: boolean;
};

declare global {
  interface Window {
    fixture?: FixtureConfig;
    calls: string[];
    navigateAway: () => Promise<void>;
  }
}
