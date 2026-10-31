import { test as base, expect, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import type { FixtureConfig } from "./fixtureTypes";

export { expect };
export const test = base.extend<{ pageErrors: string[] }>({
  pageErrors: [async ({ page }, use) => {
    const errors: string[] = [];
    page.on("pageerror", error => errors.push(error.message));
    await use(errors);
    expect(errors, "unexpected browser errors").toEqual([]);
  }, { auto: true }],
});

export async function showScreen(page: Page, config: FixtureConfig, width = 1280, options: { clock?: boolean } = {}) {
  await page.setViewportSize({ width, height: 900 });
  if (options.clock) await page.clock.install({ time: new Date("2026-10-31T09:00:00+09:00") });
  const html = await readFile(new URL("../dist/ui-test/index.html", import.meta.url), "utf8");
  await page.route("**/*", route => {
    const url = route.request().url();
    if (url === "https://ui-fixture.invalid/") {
      return route.fulfill({ contentType: "text/html", body: html.replace("<head>", `<head><script>window.fixture=${JSON.stringify(config)}</script>`) });
    }
    if (url === "https://ui-fixture.invalid/brand/wordmark.png") {
      return route.fulfill({ contentType: "image/png", path: new URL("../public/brand/wordmark.png", import.meta.url).pathname });
    }
    if (url === "https://ui-fixture.invalid/apple-touch-icon.png") {
      return route.fulfill({ contentType: "image/png", path: new URL("../public/apple-touch-icon.png", import.meta.url).pathname });
    }
    if (url === "https://ui-fixture.invalid/brand/mark.svg") {
      return route.fulfill({ contentType: "image/svg+xml", path: new URL("../public/brand/mark.svg", import.meta.url).pathname });
    }
    if (url === "https://ui-fixture.invalid/profile/rider.webp") {
      return route.fulfill({ contentType: "image/webp", path: new URL("../public/profile/rider.webp", import.meta.url).pathname });
    }
    if (url.startsWith("https://ui-fixture.invalid/api/images/")) {
      // Every registered fixture image is the 2027×254 wordmark.
      return route.fulfill({ contentType: "image/png", path: new URL("../public/brand/wordmark.png", import.meta.url).pathname });
    }
    const source = url.match(/^https:\/\/ui-fixture\.invalid\/brand\/sources\/(qiita|note|wantedly)\.png$/);
    if (source) {
      return route.fulfill({ contentType: "image/png", path: new URL(`../public/brand/sources/${source[1]}.png`, import.meta.url).pathname });
    }
    if (url.startsWith("blob:")) return route.continue();
    if (url === "https://ui-fixture.invalid/diagram.svg") {
      return route.fulfill({ contentType: "image/svg+xml", body: '<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="300"><rect width="1200" height="300" fill="#f6f7f6"/></svg>' });
    }
    return route.abort();
  });
  await page.goto("https://ui-fixture.invalid/");
  await expect(page.locator("main")).toBeVisible();
}

export async function typeArticle(page: Page, title: string, body: string) {
  await page.getByLabel("タイトル", { exact: true }).fill(title);
  await page.getByLabel("本文（Markdown）", { exact: true }).fill(body);
}

export const saveState = (page: Page) => page.locator(".save-state");

export async function noHorizontalScroll(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
}
