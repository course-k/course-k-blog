import { chromium, expect } from "@playwright/test";
import { readFile, mkdir, writeFile } from "node:fs/promises";
import assert from "node:assert/strict";
const html = await readFile(
  new URL("../dist/ui-test/index.html", import.meta.url),
  "utf8",
);
const out = new URL("../test-results/ui/", import.meta.url);
await mkdir(out, { recursive: true });
const browser = await chromium.launch({ channel: "chrome", headless: true });
const context = await browser.newContext({ offline: true });
await context.route("**/*", (route) =>
  route.request().url() === "https://ui-fixture.invalid/diagram.svg"
    ? route.fulfill({
        contentType: "image/svg+xml",
        body: '<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="300"><rect width="1200" height="300" fill="#f6f7f6"/><text x="50" y="160" font-size="40">UI test diagram</text></svg>',
      })
    : route.abort(),
);
const results = [];
async function screen(config, width = 1280) {
  const page = await context.newPage();
  await page.setViewportSize({ width, height: 900 });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.setContent(
    html.replace(
      "<head>",
      `<head><script>window.fixture=${JSON.stringify(config)}</script>`,
    ),
  );
  try {
    await expect(page.locator("main")).toBeVisible();
  } catch (e) {
    console.error("Page errors:", errors);
    console.error(await page.locator("body").innerText());
    throw e;
  }
  return { page, errors };
}
try {
  for (const width of [1280, 390, 320]) {
    const { page, errors } = await screen({ screen: "article" }, width);
    await expect(page.locator("#article-section-4")).toBeVisible();
    assert.equal(await page.locator(".reading-content h2").count(), 3);
    assert.equal(await page.locator(".reading-content h3").count(), 1);
    await expect(page.locator(".toc")).not.toContainText(
      "コード内は目次に含めない",
    );
    if (width > 900) {
      const body = await page.locator(".reading-content").boundingBox(),
        toc = await page.locator(".toc").boundingBox();
      assert(toc.x > body.x + body.width);
    } else {
      await expect(page.locator(".toc-mobile")).not.toHaveAttribute("open", "");
      await page.locator("summary").press("Enter");
      await expect(page.locator(".toc-mobile")).toHaveAttribute("open", "");
      await page.locator(".toc-mobile a").last().click();
      assert.equal(
        await page.evaluate(() => location.hash),
        "#article-section-4",
      );
    }
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    );
    assert.deepEqual(errors, []);
    await expect(page.locator(".reading-content img")).toBeVisible();
    assert(
      await page
        .locator(".reading-content img")
        .evaluate((img) => img.naturalWidth === 1200),
    );
    await page.screenshot({
      path: new URL(`article-${width}.png`, out).pathname,
      fullPage: true,
    });
    results.push(
      `article ${width}: TOC, duplicate headings, code exclusion, overflow, no script execution`,
    );
    await page.close();
  }
  for (const width of [1280, 390]) {
    const { page } = await screen({ screen: "list" }, width);
    const cards = page.locator(".post-row");
    const a = await cards.nth(0).boundingBox(),
      b = await cards.nth(1).boundingBox();
    assert.equal(a.x, b.x);
    assert(b.y > a.y + a.height);
    await expect(
      page.getByRole("link", { name: /新しいタブで開く/ }),
    ).toHaveAttribute("target", "_blank");
    await expect(
      page.getByRole("link", { name: /新しいタブで開く/ }),
    ).toHaveAttribute("rel", "noopener noreferrer");
    await cards.first().locator("a").focus();
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    );
    await page.screenshot({
      path: new URL(`list-${width}.png`, out).pathname,
      fullPage: true,
    });
    results.push(
      `list ${width}: one column, external link attributes, focus, overflow`,
    );
    await page.close();
  }
  for (const scenario of [
    "success",
    "publish-failure",
    "list-failure",
    "lost-response",
    "conflict",
  ]) {
    const { page, errors } = await screen({ screen: "admin", scenario });
    await page.getByRole("button", { name: "＋ 新しい記事" }).click();
    await page.getByLabel("タイトル", { exact: true }).fill("検証用記事");
    await page
      .getByLabel("Markdown", { exact: true })
      .fill("## 確認する見出し\n\n保存する本文。");
    await expect(page.getByRole("status")).toContainText("保存していない");
    let blocked = false;
    page.once("dialog", async (dialog) => {
      blocked = true;
      await dialog.dismiss();
    });
    await page.evaluate(() => { void window.navigateAway(); });
    await expect.poll(() => blocked).toBe(true);
    assert(blocked, "unsaved SPA navigation must ask before leaving");
    const input = await page.locator(".markdown-input").boundingBox(),
      preview = await page.locator(".preview").boundingBox();
    assert(preview.x > input.x);
    await page
      .getByRole("button", {
        name: scenario === "publish-failure" ? "公開する" : "下書きを保存",
        exact: true,
      })
      .click();
    if (scenario === "success" || scenario === "list-failure")
      await expect(page.getByRole("status")).toContainText(
        "下書きを保存しました",
      );
    if (scenario === "list-failure")
      await expect(page.getByRole("alert")).toContainText(
        "記事一覧を更新できません",
      );
    if (scenario === "publish-failure") {
      await expect(page.getByRole("alert")).toContainText(
        "下書きは保存しましたが、公開できません",
      );
      await expect(page.getByRole("status")).toContainText("下書きは保存済み");
      await expect(page.getByRole("status")).not.toContainText("進めています");
    }
    if (scenario === "lost-response") {
      await expect(page.getByRole("alert")).toContainText(
        "保存結果を確認できません",
      );
      await expect(
        page.getByRole("button", { name: "下書きを保存", exact: true }),
      ).toBeDisabled();
      await page
        .getByRole("button", { name: "保存結果を確認", exact: true })
        .click();
      await expect(page.getByRole("alert")).toContainText(
        "保存先を特定できない",
      );
      page.once("dialog", (dialog) => dialog.accept());
      await page
        .getByRole("button", { name: "検証用記事 下書き", exact: true })
        .click();
      await expect(
        page.getByRole("button", { name: "下書きを保存", exact: true }),
      ).toBeEnabled();
      assert.equal(
        (await page.evaluate(() => window.calls)).filter(
          (c) => c === "POST /api/admin/posts",
        ).length,
        1,
      );
    }
    if (scenario === "conflict")
      await expect(page.getByRole("alert")).toContainText("競合しました");
    await expect(page.getByLabel("Markdown", { exact: true })).toHaveValue(
      "## 確認する見出し\n\n保存する本文。",
    );
    if (scenario === "success") {
      await page.screenshot({
        path: new URL("admin-desktop.png", out).pathname,
        fullPage: true,
      });
      await page.getByRole("button", { name: "公開する", exact: true }).click();
      await expect(page.getByRole("status")).toContainText("公開しました");
      await expect(
        page.getByRole("button", { name: "更新を公開する" }),
      ).toBeEnabled();
      await page.setViewportSize({ width: 390, height: 844 });
      await expect(page.locator(".preview")).toBeHidden();
      await page
        .getByRole("button", { name: "プレビュー", exact: true })
        .click();
      await expect(page.locator(".preview")).toBeVisible();
      await expect(
        page
          .getByRole("region", { name: "プレビュー" })
          .getByRole("heading", { name: "確認する見出し" }),
      ).toBeVisible();
      assert(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      );
      await page.screenshot({
        path: new URL("admin-mobile.png", out).pathname,
        fullPage: true,
      });
    }
    assert.deepEqual(errors, []);
    results.push(`admin ${scenario}: passed`);
    await page.close();
  }
  const { page } = await screen({ screen: "list", empty: true });
  await expect(page.getByText("最初の記事を準備しています")).toBeVisible();
  await page.close();
  results.push("empty list: passed");
  await writeFile(
    new URL("results.json", out),
    JSON.stringify(
      { mode: "offline components; mock API, not live integration", results },
      null,
      2,
    ),
  );
  console.log(results.join("\n"));
} finally {
  await browser.close();
}
