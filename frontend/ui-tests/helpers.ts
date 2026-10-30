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

export async function showScreen(page: Page, config: FixtureConfig, width = 1280) {
  await page.setViewportSize({ width, height: 900 });
  const html = await readFile(new URL("../dist/ui-test/index.html", import.meta.url), "utf8");
  await page.route("**/*", route => {
    const url = route.request().url();
    if (url === "https://ui-fixture.invalid/") {
      return route.fulfill({ contentType: "text/html", body: html.replace("<head>", `<head><script>window.fixture=${JSON.stringify(config)}</script>`) });
    }
    if (url === "https://ui-fixture.invalid/brand/wordmark.png") {
      return route.fulfill({ contentType: "image/png", path: new URL("../public/brand/wordmark.png", import.meta.url).pathname });
    }
    if (url === "https://ui-fixture.invalid/diagram.svg") {
      return route.fulfill({ contentType: "image/svg+xml", body: '<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="300"><rect width="1200" height="300" fill="#f6f7f6"/></svg>' });
    }
    return route.abort();
  });
  await page.goto("https://ui-fixture.invalid/");
  await expect(page.locator("main")).toBeVisible();
}

export async function startArticle(page: Page) {
  await page.getByRole("button", { name: "＋ 新しい記事" }).click();
  await page.getByLabel("タイトル", { exact: true }).fill("検証用記事");
  await page.getByLabel("Markdown", { exact: true }).fill("## 確認する見出し\n\n保存する本文。");
}

export async function publishArticle(page: Page) {
  await page.getByRole("button", { name: "公開する", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("公開しました");
}

export async function loseNextResponse(page: Page, suffix: string) {
  await page.evaluate(suffix => {
    const original = window.fetch;
    let lost = false;
    window.fetch = async (...args) => {
      const response = await original(...args);
      const method = args[1]?.method ?? "GET";
      if (!lost && method !== "GET" && String(args[0]).endsWith(suffix)) {
        lost = true;
        throw new TypeError("fixture: response lost after commit");
      }
      return response;
    };
  }, suffix);
}
