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
  const saved = page.locator(".save-state");
  const markdownInput = page.getByLabel("本文（Markdown）", { exact: true });
  await page.goto("/admin");
  await page.getByLabel("パスワード", { exact: true }).fill(password);
  await page.getByRole("button", { name: "ログイン", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: "新規作成" })).toBeVisible();
  await expect(page.getByRole("button", { name: "下書きを保存" })).toHaveCount(0);
  await page.getByLabel("タイトル", { exact: true }).fill(title);
  await markdownInput.fill("## 公開前のメモ\n\n下書きの内容です。");
  await expect(
    page
      .getByRole("region", { name: "プレビュー" })
      .getByRole("heading", { name: "公開前のメモ" }),
  ).toBeVisible();
  // Autosave: no button; the URL switches to the created article.
  await expect(saved).toHaveText("保存済み", { timeout: 15_000 });
  await expect(page).toHaveURL(/\/admin\?view=edit&id=[A-Za-z0-9_-]{43}$/);
  const pixel = Buffer.from(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==",
    "base64",
  );
  const webp = Buffer.from("UklGRiIAAABXRUJQVlA4TBYAAAAvAkAAEA8Q8x+DHAwFaRuwsN2J6H88", "base64");
  const upload = async (name: string, bytes: Buffer, type = "image/png") => {
    await markdownInput.evaluate((element: HTMLTextAreaElement) => {
      element.focus();
      element.setSelectionRange(element.value.length, element.value.length);
    });
    await page.evaluate(
      ({ name, data, type }) => {
        const bytes = Uint8Array.from(atob(data), (c) => c.charCodeAt(0));
        const transfer = new DataTransfer();
        transfer.items.add(new File([bytes], name, { type }));
        document.getElementById("markdown")!.dispatchEvent(
          new ClipboardEvent("paste", { clipboardData: transfer, bubbles: true, cancelable: true }),
        );
      },
      { name, data: bytes.toString("base64"), type },
    );
    await expect(markdownInput).not.toHaveValue(/pending-image:/, { timeout: 20_000 });
    const paths = (await markdownInput.inputValue()).match(/\/api\/images\/[A-Za-z0-9_-]{43}/g) ?? [];
    expect(paths.length).toBeGreaterThan(0);
    await expect(saved).toHaveText("保存済み", { timeout: 15_000 });
    return paths.at(-1)!;
  };
  const firstImage = await upload("first.png", pixel);
  const webpImage = await upload("transparent.webp", webp, "image/webp");
  expect((await page.request.get(webpImage)).headers()["content-type"]).toBe("image/png");
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
  expect((await page.request.get(`/og/posts/${draft.id}.png`)).status()).toBe(404);
  // Lay out the first image in the preview; the setting is autosaved with the text.
  await page.locator(".preview .blog-image img").first().click();
  await page.getByRole("toolbar", { name: "画像の配置とサイズ" }).getByRole("button", { name: "中央寄せ" }).click();
  await expect(markdownInput).toHaveValue(/blog-image:v1;width=auto;align=center/);
  await expect(saved).toHaveText("保存済み", { timeout: 15_000 });
  await page.getByRole("button", { name: "公開する", exact: true }).click();
  await expect(page.getByText("公開しました。")).toBeVisible();
  const response = await reading.goto(`/posts/${draft.id}`);
  expect(response?.status()).toBe(200);
  await expect(
    reading.getByRole("heading", { name: title, exact: true }),
  ).toBeVisible();
  await expect(
    reading.getByText("下書きの内容です。", { exact: true }),
  ).toBeVisible();
  expect((await reading.request.get(firstImage)).status()).toBe(200);
  await expect(reading.locator(".reading-content .blog-image").first()).toHaveAttribute("data-align", "center");
  const viewer = await browser.newContext();
  const viewing = await viewer.newPage();
  await viewing.goto(`/posts/${draft.id}`);
  await viewing.locator(".reading-content .blog-image img").first().click();
  await expect(viewing.getByRole("dialog")).toBeVisible();
  await viewing.keyboard.press("Escape");
  await expect(viewing.getByRole("dialog")).toHaveCount(0);
  await viewer.close();
  const firstPublic = await (
    await reading.request.get(`/api/posts/${draft.id}`)
  ).json();
  await expect(reading.locator('meta[property="og:title"]')).toHaveAttribute('content', title);
  await expect(reading.locator('meta[property="og:type"]')).toHaveAttribute('content', 'article');
  const ogp = await reading.request.get(`/og/posts/${draft.id}.png`);
  expect(ogp.status()).toBe(200);
  expect(ogp.headers()['content-type']).toBe('image/png');
  // The gateway and application may both add the same no-store directive.
  expect(new Set(ogp.headers()['cache-control'].split(',').map(value => value.trim())))
    .toEqual(new Set(['no-store']));
  const png = await ogp.body();
  expect(png.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))).toBe(true);
  expect([png.readUInt32BE(16), png.readUInt32BE(20)]).toEqual([1200, 630]);
  await markdownInput.fill("## 更新後のメモ\n\nまだ公開していない変更です。");
  const secondImage = await upload("second.png", pixel);
  await expect(page.locator(".publication-state")).toHaveText("公開中・未公開の変更あり");
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
  await expect(page.getByText("公開しました。")).toBeVisible();
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
  const wordmark = reading.getByRole('heading', { level: 1, name: 'course_kのブログ' }).getByRole('img');
  await expect(wordmark).toHaveAttribute('src', '/brand/wordmark.png');
  expect(await wordmark.evaluate(img => (img as HTMLImageElement).naturalWidth)).toBe(2027);
  await expect(reading.getByRole('img', { name: 'バイクに乗るcourse_k', exact: true })).toHaveAttribute('src', '/profile/rider.webp');
  await expect(reading.getByRole('link', { name: 'course_kのブログのトップへ', exact: true }).locator('img')).toHaveAttribute('src', '/brand/mark.svg');
  for (const asset of ['/favicon.svg', '/favicon.ico', '/apple-touch-icon.png', '/brand/wordmark.png', '/brand/ogp-home.png', '/brand/mark.svg', '/profile/rider.webp']) {
    const response = await reading.request.get(asset);
    expect(response.status(), asset).toBe(200);
    expect(response.headers()['content-type'], asset).toMatch(/^image\//);
  }
  await expect(
    reading.getByRole("link", { name: new RegExp(title) }),
  ).toBeVisible();
  // Legacy corrections and hidden flags are no longer applied to fetched articles.
  await expect(
    reading.getByRole("link", {
      name: "外部の記事の元タイトル — Qiita、新しいタブで開く",
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
  await expect(page.getByText("非公開にしました。")).toBeVisible();
  expect((await reading.request.get(`/api/posts/${draft.id}`)).status()).toBe(
    404,
  );
  expect((await reading.request.get(secondImage)).status()).toBe(404);
  expect((await reading.request.get(`/og/posts/${draft.id}.png`)).status()).toBe(404);
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
  await page.getByRole("navigation", { name: "管理メニュー" }).getByRole("link", { name: "記事一覧" }).click();
  await expect(page.getByRole("link", { name: title })).toBeVisible();
  await page.getByRole("navigation", { name: "管理メニュー" }).getByRole("link", { name: "設定" }).click();
  await page.getByRole("button", { name: "Qiitaを同期", exact: true }).click();
  await expect(page.getByText("Qiitaの同期を待っています…")).toBeVisible();
  await page.getByRole("button", { name: "ログアウト", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "管理者ログイン" }),
  ).toBeVisible();
  expect((await page.request.get("/api/admin/posts")).status()).toBe(401);
  expect(errors).toEqual([]);
  await reader.close();
});
