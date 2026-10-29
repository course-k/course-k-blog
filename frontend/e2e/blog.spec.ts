import { expect, test } from "@playwright/test";
import { readFile } from "node:fs/promises";

test("reader and owner flow against real MySQL and production SSR", async ({
  page,
  browser,
}) => {
  if (process.env.RUN_LOCAL_E2E !== "1") {
    throw new Error(
      "専用の破棄可能なローカル環境を用意し、RUN_LOCAL_E2E=1を指定してください。記事の作成・公開を行います。",
    );
  }
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const login = await readFile(
    process.env.BLOG_E2E_LOGIN_FILE ||
      new URL("../../config/local-login.txt", import.meta.url),
    "utf8",
  );
  const password = login.match(/^Password: (.+)$/m)?.[1];
  if (!password) throw new Error("Local fixture credentials are missing");
  const title = `動作確認用の記事 ${Date.now()}`;
  await page.goto("/admin");
  await page.getByLabel("パスワード", { exact: true }).fill(password);
  await page.getByRole("button", { name: "ログイン", exact: true }).click();
  await expect(page.getByRole("heading", { name: "記事を書く" })).toBeVisible();
  const external = page.getByRole("region", {
    name: "外部の記事",
    exact: true,
  });
  await external
    .getByLabel("補正する外部記事")
    .selectOption("qiita:e2e-external");
  await external
    .getByLabel("タイトルの補正", { exact: false })
    .fill("外部記事の表示確認");
  await external
    .getByRole("button", { name: "外部記事の設定を保存", exact: true })
    .click();
  await expect(external.getByLabel("補正する外部記事")).toHaveValue("");
  await page.getByRole("button", { name: "＋ 新しい記事" }).click();
  await page.getByLabel("タイトル", { exact: true }).fill(title);
  await page
    .getByLabel("Markdown", { exact: true })
    .fill("## 公開前のメモ\n\n下書きの内容です。");
  await expect(
    page
      .getByRole("region", { name: "プレビュー" })
      .getByRole("heading", { name: "公開前のメモ" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "下書きを保存", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("下書きを保存しました");
  const pixel = Buffer.from(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==",
    "base64",
  );
  const upload = async (name: string) => {
    await page
      .getByLabel("画像を挿入（PNG/JPEG、5MiB以内）", { exact: true })
      .setInputFiles({ name, mimeType: "image/png", buffer: pixel });
    await expect(page.getByRole("status")).toContainText("画像を挿入しました");
    const markdown = await page
      .getByLabel("Markdown", { exact: true })
      .inputValue();
    const path = markdown.match(/\/api\/images\/[A-Za-z0-9_-]{43}/)?.[0];
    expect(path).toBeTruthy();
    return path!;
  };
  const firstImage = await upload("first.png");
  const adminResponse = await page.request.get("/api/admin/posts");
  expect(adminResponse.status()).toBe(200);
  const draft = (await adminResponse.json()).find(
    (p: { title: string }) => p.title === title,
  );
  expect(draft).toBeTruthy();
  const reader = await browser.newContext({ javaScriptEnabled: false });
  const reading = await reader.newPage();
  expect((await reading.request.get(`/api/posts/${draft.id}`)).status()).toBe(
    404,
  );
  expect((await reading.request.get(firstImage)).status()).toBe(404);
  expect((await page.request.get(firstImage)).status()).toBe(200);
  await page.getByRole("button", { name: "公開する", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("公開しました");
  const response = await reading.goto(`/posts/${draft.id}`);
  expect(response?.status()).toBe(200);
  await expect(
    reading.getByRole("heading", { name: title, exact: true }),
  ).toBeVisible();
  await expect(
    reading.getByText("下書きの内容です。", { exact: true }),
  ).toBeVisible();
  expect((await reading.request.get(firstImage)).status()).toBe(200);
  const firstPublic = await (
    await reading.request.get(`/api/posts/${draft.id}`)
  ).json();
  await page
    .getByLabel("Markdown", { exact: true })
    .fill("## 更新後のメモ\n\nまだ公開していない変更です。");
  const secondImage = await upload("second.png");
  await page.getByRole("button", { name: "下書きを保存", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("下書きを保存しました");
  expect((await reading.request.get(firstImage)).status()).toBe(200);
  expect((await reading.request.get(secondImage)).status()).toBe(404);
  await reading.reload();
  await expect(
    reading.getByText("下書きの内容です。", { exact: true }),
  ).toBeVisible();
  await expect(
    reading.getByText("まだ公開していない変更です。", { exact: true }),
  ).toHaveCount(0);
  await page
    .getByRole("button", { name: "更新を公開する", exact: true })
    .click();
  await expect(page.getByRole("status")).toContainText("公開しました");
  await reading.reload();
  await expect(
    reading.getByText("まだ公開していない変更です。", { exact: true }),
  ).toBeVisible();
  expect((await reading.request.get(firstImage)).status()).toBe(404);
  expect((await reading.request.get(secondImage)).status()).toBe(200);
  const updatedPublic = await (
    await reading.request.get(`/api/posts/${draft.id}`)
  ).json();
  expect(updatedPublic.publishedAt).toBe(firstPublic.publishedAt);
  await reading.setViewportSize({ width: 1280, height: 900 });
  await reading.goto("/");
  await expect(
    reading.getByRole("link", { name: new RegExp(title) }),
  ).toBeVisible();
  await expect(
    reading.getByRole("link", {
      name: "外部記事の表示確認 — Qiita、新しいタブで開く",
      exact: true,
    }),
  ).toHaveAttribute("href", "https://qiita.com/course_k/items/e2e-external");
  // Exercise the real archive, SSR and database without client-side JavaScript.
  const parts = new Intl.DateTimeFormat("en", { timeZone: "Asia/Tokyo", year: "numeric", month: "2-digit" }).formatToParts(new Date(firstPublic.publishedAt));
  const year = parts.find(p => p.type === "year")!.value;
  const monthNumber = parts.find(p => p.type === "month")!.value;
  const month = `${year}-${monthNumber}`;
  const archive = reading.locator(".month-archive");
  await archive.locator(":scope > summary").click();
  const yearGroup = archive.locator("details").filter({ has: reading.locator("summary", { hasText: `${year}年` }) });
  if (await yearGroup.getAttribute("open") === null) await yearGroup.locator("summary").click();
  await yearGroup.getByRole("link", { name: new RegExp(`^${Number(monthNumber)}月 `) }).click();
  await expect(reading).toHaveURL(new RegExp(`month=${month}`));
  await expect(reading.getByRole("heading", { name: `${year}年${Number(monthNumber)}月の記事`, exact: true })).toBeVisible();
  await expect(reading.getByRole("link", { name: new RegExp(title) })).toBeVisible();
  const monthlyResponse = await reading.request.get(`/api/articles?month=${month}`);
  expect(monthlyResponse.status()).toBe(200);
  const monthlyData = await monthlyResponse.json();
  expect(monthlyData.articles.some((a: { id: string }) => a.id === draft.id)).toBe(true);
  expect(monthlyData.months.some((m: { month: string; count: number }) => m.month === month && m.count > 0)).toBe(true);
  await reading.getByRole("link", { name: "すべての記事に戻る", exact: true }).click();
  await expect(reading).toHaveURL(/\/$/);
  await reading.screenshot({
    path: "test-results/e2e/desktop.png",
    fullPage: true,
  });
  await reading.setViewportSize({ width: 390, height: 844 });
  await reading.screenshot({
    path: "test-results/e2e/mobile.png",
    fullPage: true,
  });
  expect(
    await reading.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: "test-results/e2e/editor.png",
    fullPage: true,
  });
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "非公開に戻す", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("非公開にしました");
  expect((await reading.request.get(`/api/posts/${draft.id}`)).status()).toBe(
    404,
  );
  expect((await reading.request.get(secondImage)).status()).toBe(404);
  const operations = await (
    await page.request.get("/api/admin/operations")
  ).json();
  expect(operations.backups.length).toBeGreaterThan(0);
  expect(operations.state.lastSuccess).toBeTruthy();
  expect(operations.state.lastError).toBe("");
  const hidden = await reading.goto(`/posts/${draft.id}`);
  expect(hidden?.status()).toBe(404);
  await expect(
    reading.getByRole("heading", { name: "記事が見つかりません" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "ログアウト", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "管理者ログイン" }),
  ).toBeVisible();
  expect((await page.request.get("/api/admin/posts")).status()).toBe(401);
  expect(errors).toEqual([]);
  await reader.close();
});
