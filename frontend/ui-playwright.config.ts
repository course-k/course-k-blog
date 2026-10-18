import { defineConfig } from "@playwright/test";

const channel = process.env.PLAYWRIGHT_CHANNEL || "chrome";
if (!["chrome", "chromium"].includes(channel)) {
  throw new Error("Choose chrome or chromium explicitly.");
}
export default defineConfig({
  testDir: "./ui-tests",
  testMatch: "**/*.spec.ts",
  workers: 1,
  timeout: 20_000,
  outputDir: "./test-results/ui",
  reporter: "list",
  use: { channel: channel === "chrome" ? "chrome" : undefined, offline: true },
});
