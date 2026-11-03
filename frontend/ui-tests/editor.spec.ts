import type { Page } from "@playwright/test";
import { test, expect, showScreen, typeArticle, saveState, noHorizontalScroll } from "./helpers";
import type { FixtureConfig } from "./fixtureTypes";

const admin = (page: Page, config: Partial<FixtureConfig> = {}, width = 1280, clock = true) =>
  showScreen(page, { screen: "admin", ...config }, width, { clock });
const writes = (page: Page) =>
  page.evaluate(() => window.fixtureServer.calls.filter((call) => /^(POST|PUT) \/api\/admin\/posts/.test(call) && !/publish/.test(call)));
const body = (page: Page) => page.getByLabel("本文（Markdown）", { exact: true });
const title = (page: Page) => page.getByLabel("タイトル", { exact: true });
const nav = (page: Page) => page.getByRole("navigation", { name: "管理メニュー" });
const searchOf = (page: Page) =>
  page.evaluate(() => (window as unknown as { router: { state: { location: { search: Record<string, string> } } } }).router.state.location.search);

test("opens on an empty new article without creating one or showing removed controls", async ({ page }) => {
  await admin(page);
  await expect(page.getByRole("heading", { level: 1, name: "新規作成" })).toBeVisible();
  await expect(title(page)).toHaveValue("");
  await expect(body(page)).toHaveValue("");
  await expect(body(page)).toBeEditable();
  await page.clock.runFor(60_000);
  expect(await writes(page)).toEqual([]);
  for (const removed of ["下書きを保存", "画像を挿入", "＋ 新しい記事", "ログアウト", "記事一覧を表示"])
    await expect(page.getByRole("button", { name: removed })).toHaveCount(0);
  await expect(page.locator('input[type="file"], input[type="number"]')).toHaveCount(0);
  for (const text of ["未入力", "書きたいことを、ここから。", "記事を選ぶか、新しい記事を作成してください。", "編集内容は自動保存されません。"])
    await expect(page.getByText(text)).toHaveCount(0);
  await expect(nav(page).getByRole("link", { name: "新規作成" })).toHaveAttribute("aria-current", "page");
});

test("menu switches between new, list and settings; only settings has sync, backup and logout", async ({ page }) => {
  await admin(page, { posts: [{ title: "既存の記事" }] });
  const editor = page.locator(".article-editor");
  for (const text of ["バックアップ", "外部記事の同期", "ログアウト", "既存の記事"])
    await expect(editor.getByText(text)).toHaveCount(0);
  await nav(page).getByRole("link", { name: "記事一覧" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "記事一覧" })).toBeVisible();
  await expect(page.getByRole("link", { name: "既存の記事" })).toBeVisible();
  await nav(page).getByRole("link", { name: "設定" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "設定" })).toBeVisible();
  const icons = page.getByRole("group", { name: "媒体ごとの同期" }).getByRole("button");
  await expect(icons).toHaveCount(3);
  expect(await icons.evaluateAll((buttons) => buttons.map((button) => button.getAttribute("aria-label")))).toEqual([
    "Qiitaを同期",
    "Wantedlyを同期",
    "noteを同期",
  ]);
  await expect(page.getByRole("heading", { name: "バックアップ" })).toBeVisible();
  await expect(page.getByRole("button", { name: "ログアウト" })).toBeVisible();
  for (const removed of ["外部記事の状態を再取得", "バックアップの状態を再取得", "すべて同期"])
    await expect(page.getByRole("button", { name: removed })).toHaveCount(0);
  await nav(page).getByRole("link", { name: "新規作成" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "新規作成" })).toBeVisible();
});

for (const width of [1440, 1024, 768, 390, 320]) {
  test(`editor layout ${width}: side by side on desktop, switchable when narrow, no overflow`, async ({ page }) => {
    await admin(page, {}, width);
    await typeArticle(page, "長いタイトル".repeat(30), "## 見出し\n\n" + "本文".repeat(400));
    const input = (await body(page).boundingBox())!;
    const preview = page.getByRole("region", { name: "プレビュー" });
    if (width >= 768) {
      const box = (await preview.boundingBox())!;
      expect(box.x).toBeGreaterThan(input.x + input.width - 1);
      expect(Math.abs(box.width - input.width)).toBeLessThan(24);
      await expect(page.getByRole("group", { name: "表示の切り替え" })).toBeHidden();
    } else {
      await expect(preview).toBeHidden();
      await page.getByRole("button", { name: "プレビュー", exact: true }).click();
      await expect(preview).toBeVisible();
      await expect(body(page)).toBeHidden();
    }
    await expect(page.getByRole("button", { name: "公開する" })).toBeVisible();
    await noHorizontalScroll(page);
    await nav(page).getByRole("link", { name: "設定" }).click();
    await page.clock.runFor(1_000);
    await expect(page.getByRole("button", { name: "ログアウト" })).toBeVisible();
    await noHorizontalScroll(page);
  });
}

test("article list shows own articles only, newest update first, with states and untitled rows", async ({ page }) => {
  const long = "とても長いタイトル".repeat(20);
  await admin(page, {
    path: "/admin?view=articles",
    posts: [
      { title: "古い下書き", updatedAt: "2026-10-01T00:00:00Z" },
      { title: "", updatedAt: "2026-10-03T00:00:00Z" },
      { title: long, updatedAt: "2026-10-02T00:00:00Z", published: true, publishedAt: "2026-10-02T00:00:00Z" },
      { id: "z".repeat(43), title: "同時刻Z", updatedAt: "2026-10-04T00:00:00Z" },
      { id: "y".repeat(43), title: "同時刻Y", updatedAt: "2026-10-04T00:00:00Z" },
    ],
  }, 390);
  const rows = page.locator(".article-table tbody tr");
  await expect(rows).toHaveCount(5);
  expect(await rows.locator("td:first-child").allInnerTexts()).toEqual(["同時刻Y", "同時刻Z", "無題", long, "古い下書き"]);
  await expect(rows.nth(3)).toContainText("公開中");
  await expect(rows.nth(0)).toContainText("下書き");
  await expect(rows.nth(0)).toContainText("2026/10/04 09:00");
  await noHorizontalScroll(page);
  await page.evaluate(() => {
    const posts = window.fixtureServer.posts;
    const id = Object.keys(posts).find((key) => posts[key].title === "古い下書き")!;
    posts[id] = { ...posts[id], title: "サーバーで更新済み", version: 7 };
  });
  await page.getByRole("link", { name: "古い下書き" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "記事を編集" })).toBeVisible();
  await expect(title(page)).toHaveValue("サーバーで更新済み");
  await expect(nav(page).getByRole("link", { name: "記事一覧" })).toHaveAttribute("aria-current", "page");
});

test("empty article list", async ({ page }) => {
  await admin(page, { path: "/admin?view=articles" });
  await expect(page.getByText("記事はありません", { exact: true })).toBeVisible();
});

test("unknown views fall back to new; a bad article ID reports an error", async ({ page }) => {
  await admin(page, { path: "/admin?view=nope" });
  await expect(page.getByRole("heading", { level: 1, name: "新規作成" })).toBeVisible();
  await page.evaluate(() => (window as unknown as { router: { navigate(o: unknown): Promise<void> } }).router.navigate({ to: "/admin", search: { view: "edit", id: "b".repeat(43) } }));
  await expect(page.getByText("指定された記事は見つかりません。")).toBeVisible();
  await expect(page.getByRole("link", { name: "記事一覧へ" })).toBeVisible();
});

test("autosave starts 3 seconds after the last edit, then replaces the URL with the new ID", async ({ page }) => {
  await admin(page);
  await body(page).fill("タイトルなしの本文");
  await expect(saveState(page)).toHaveText("未保存");
  await page.clock.runFor(2_900);
  expect(await writes(page)).toEqual([]);
  await page.clock.runFor(100);
  await expect(saveState(page)).toHaveText("保存済み");
  expect(await writes(page)).toEqual(["POST /api/admin/posts"]);
  await expect(page.getByRole("heading", { level: 1, name: "記事を編集" })).toBeVisible();
  const search = await searchOf(page);
  expect(search.view).toBe("edit");
  expect(search.id).toMatch(/^[0-9n]{43}$/);
  expect(await page.evaluate(() => (window as unknown as { router: { history: { length: number } } }).router.history.length)).toBe(1);
  await expect(body(page)).toHaveValue("タイトルなしの本文");
  await expect(page.getByRole("button", { name: "公開する" })).toBeDisabled();
  await expect(page.getByText("公開にはタイトルが必要です。")).toBeVisible();
  await page.clock.runFor(60_000);
  expect(await writes(page)).toHaveLength(1);
});

test("continuous typing is saved at 30 seconds and input stays enabled during a slow save", async ({ page }) => {
  await admin(page);
  await body(page).focus();
  for (let second = 0; second < 29; second++) {
    await page.keyboard.type("あ");
    await page.clock.runFor(1_000);
  }
  await page.keyboard.type("い");
  await page.clock.runFor(900);
  expect(await writes(page)).toEqual([]);
  await page.evaluate(() => window.fixtureServer.fail({ method: "POST", path: "/api/admin/posts", mode: "hold" }));
  await page.keyboard.type("う");
  await page.clock.runFor(100);
  await expect(saveState(page)).toHaveText("保存中…");
  await expect(body(page)).toBeEditable();
  await page.keyboard.type("え");
  await page.evaluate(() => window.fixtureServer.release());
  await page.clock.runFor(3_000);
  await expect(saveState(page)).toHaveText("保存済み");
  await expect(body(page)).toHaveValue("あ".repeat(29) + "いうえ");
  const saved = await page.evaluate(() => Object.values(window.fixtureServer.posts)[0].markdown);
  expect(saved).toBe("あ".repeat(29) + "いうえ");
});

test("lost creation response is resolved by its key without a duplicate article", async ({ page }) => {
  await admin(page);
  await page.evaluate(() => window.fixtureServer.fail({ method: "POST", path: "/api/admin/posts", mode: "lost" }));
  await typeArticle(page, "題", "本文");
  await page.clock.runFor(3_000);
  await expect(saveState(page)).toHaveText("保存済み");
  expect(await page.evaluate(() => Object.keys(window.fixtureServer.posts).length)).toBe(1);
  expect(await page.evaluate(() => window.fixtureServer.calls.some((call) => call.includes("/creation-results/")))).toBe(true);
});

test("conflict keeps local input, never overwrites automatically and offers explicit choices", async ({ page }) => {
  await admin(page, { path: `/admin?view=edit&id=${"b".repeat(43)}`, posts: [{ title: "題", markdown: "元" }] });
  await expect(body(page)).toHaveValue("元");
  await page.evaluate(() => {
    const post = Object.values(window.fixtureServer.posts)[0];
    window.fixtureServer.posts[post.id] = { ...post, markdown: "別タブの変更", version: 5 };
  });
  await body(page).fill("この画面の変更");
  await page.clock.runFor(3_000);
  await expect(page.getByRole("heading", { name: "保存先が別の画面で更新されています" })).toBeVisible();
  await page.clock.runFor(120_000);
  expect(await page.evaluate(() => Object.values(window.fixtureServer.posts)[0].markdown)).toBe("別タブの変更");
  await page.getByText("サーバー版を表示（版5）").click();
  await expect(page.locator(".conflict-panel pre")).toHaveText("別タブの変更");
  page.once("dialog", (dialog) => void dialog.accept());
  await page.getByRole("button", { name: "この画面の内容で上書き保存" }).click();
  await page.clock.runFor(100);
  await expect(saveState(page)).toHaveText("保存済み");
  expect(await page.evaluate(() => Object.values(window.fixtureServer.posts)[0].markdown)).toBe("この画面の変更");
});

test("expired session keeps input, stops sending and resumes after login", async ({ page }) => {
  await admin(page, { path: `/admin?view=edit&id=${"b".repeat(43)}`, posts: [{ title: "題", markdown: "元" }] });
  await page.evaluate(() => {
    window.fixtureServer.signedIn = false;
  });
  await body(page).fill("期限切れ中の入力");
  await page.clock.runFor(3_000);
  await expect(page.getByRole("heading", { name: "管理者ログイン" })).toBeVisible();
  await expect(page.getByText("再ログインが必要です。編集中の入力は保持しています。")).toBeVisible();
  await page.getByLabel("パスワード").fill("fixture-password");
  await page.getByRole("button", { name: "ログイン" }).click();
  await expect(body(page)).toHaveValue("期限切れ中の入力");
  await page.clock.runFor(100);
  await expect(saveState(page)).toHaveText("保存済み");
  expect(await page.evaluate(() => Object.values(window.fixtureServer.posts)[0].markdown)).toBe("期限切れ中の入力");
});

test("menu navigation saves first; failure stops and offers continue, retry or discard", async ({ page }) => {
  await admin(page);
  await typeArticle(page, "題", "移動前の本文");
  await nav(page).getByRole("link", { name: "記事一覧" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "記事一覧" })).toBeVisible();
  expect(await page.evaluate(() => Object.values(window.fixtureServer.posts)[0].markdown)).toBe("移動前の本文");
  await page.getByRole("link", { name: "題" }).click();
  await body(page).fill("保存できない変更");
  await page.evaluate(() => window.fixtureServer.fail({ method: "PUT", path: "", mode: "status", status: 400, body: { error: "入力を確認してください。" } }));
  await nav(page).getByRole("link", { name: "設定" }).click();
  const dialog = page.getByRole("alertdialog");
  await expect(dialog).toContainText("保存できていない変更があります");
  await dialog.getByRole("button", { name: "編集を続ける" }).click();
  await expect(body(page)).toHaveValue("保存できない変更");
  await expect(page.getByRole("heading", { level: 1, name: "記事を編集" })).toBeVisible();
  await nav(page).getByRole("link", { name: "設定" }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "変更を破棄して移動" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "設定" })).toBeVisible();
  expect(await page.evaluate(() => Object.keys(window.fixtureServer.posts).length)).toBe(1);
});

test("unknown outcome while leaving warns that the server may have saved", async ({ page }) => {
  await admin(page);
  await typeArticle(page, "題", "本文");
  await page.clock.runFor(3_000);
  await page.evaluate(() => {
    window.fixtureServer.fail({ method: "PUT", path: "", mode: "before" });
    window.fixtureServer.fail({ method: "GET", path: "", mode: "before", times: 5 });
  });
  await body(page).fill("本文の続き");
  await nav(page).getByRole("link", { name: "設定" }).click();
  await expect(page.getByRole("alertdialog")).toContainText("サーバーでは保存済みの可能性があります。");
  await page.getByRole("alertdialog").getByRole("button", { name: "編集を続ける" }).click();
  await expect(saveState(page)).toContainText("再試行します");
});

test("publish confirms the latest text first; autosave after publication leaves readers' version alone", async ({ page }) => {
  await admin(page);
  await typeArticle(page, "公開する記事", "初版");
  await page.getByRole("button", { name: "公開する" }).click();
  await expect(page.locator(".publication-state")).toHaveText("公開中");
  const calls = await page.evaluate(() => window.fixtureServer.calls.filter((call) => /admin\/posts/.test(call)));
  expect(calls.indexOf("POST /api/admin/posts")).toBeLessThan(calls.findIndex((call) => call.endsWith("/publish")));
  await body(page).fill("第二版");
  await page.clock.runFor(3_000);
  await expect(page.locator(".publication-state")).toHaveText("公開中・未公開の変更あり");
  await expect(page.getByRole("button", { name: "更新を公開する" })).toBeEnabled();
  expect(await page.evaluate(() => Object.values(window.fixtureServer.posts)[0].markdown)).toBe("第二版");
  page.once("dialog", (dialog) => void dialog.accept());
  await page.getByRole("button", { name: "非公開に戻す" }).click();
  await expect(page.locator(".publication-state")).toHaveText("下書き");
});

test("offline input survives reload and is restored after confirmation", async ({ page }) => {
  await admin(page);
  await page.evaluate(() => window.fixtureServer.fail({ method: "POST", path: "/api/admin/posts", mode: "before", times: 10 }));
  await typeArticle(page, "オフラインの記事", "通信できない間の本文");
  await page.clock.runFor(4_000);
  await expect(saveState(page)).toContainText("保存できませんでした。再試行します。");
  await page.reload();
  await expect(page.getByRole("heading", { name: "前回の編集内容を復元しますか？" })).toBeVisible();
  await expect(page.locator(".recovery-prompt")).toContainText("オフラインの記事");
  await expect(body(page)).toHaveValue("");
  await page.getByRole("button", { name: "復元" }).click();
  await expect(title(page)).toHaveValue("オフラインの記事");
  await expect(body(page)).toHaveValue("通信できない間の本文");
  await page.clock.runFor(100);
  await expect(saveState(page)).toHaveText("保存済み");
});

async function pasteImages(page: Page, names: string[]) {
  await page.evaluate(async (names) => {
    const source = new Blob([Uint8Array.from(atob("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="), (c) => c.charCodeAt(0))], { type: "image/png" });
    const transfer = new DataTransfer();
    for (const name of names) transfer.items.add(new File([source], name, { type: name.endsWith(".gif") ? "image/gif" : "image/png" }));
    const area = document.getElementById("markdown") as HTMLTextAreaElement;
    area.dispatchEvent(new ClipboardEvent("paste", { clipboardData: transfer, bubbles: true, cancelable: true }));
  }, names);
}

test("pasted images replace the selection, keep order when one fails and never send pending slots", async ({ page }) => {
  await admin(page, {}, 1280, false);
  await page.evaluate(() => {
    window.fixtureServer.uploadFailures["two.png"] = "pixels";
  });
  await typeArticle(page, "画像", "前の文 置換される語 後の文");
  await body(page).evaluate((element: HTMLTextAreaElement) => element.setSelectionRange(4, 10));
  await pasteImages(page, ["one.png", "two.png", "three.png", "anim.gif"]);
  await expect(page.locator(".error")).toContainText("SVG・GIF・HEICは対象外");
  await expect(page.locator(".pending-image-actions")).toContainText("pixels");
  await expect.poll(() => body(page).inputValue()).toMatch(/^前の文 \n\n!\[one\]\(\/api\/images\/\w+\)\n\n!\[two\]\(pending-image:[\w-]+\)\n\n!\[three\]\(\/api\/images\/\w+\)\n\n 後の文$/);
  await expect(saveState(page)).toContainText("画像の送信に失敗しました。");
  await expect(page.getByRole("button", { name: "公開する" })).toBeDisabled();
  await expect.poll(() => page.evaluate(() => Object.values(window.fixtureServer.posts)[0]?.markdown ?? ""), { timeout: 8_000 }).toContain("![three]");
  const saved = await page.evaluate(() => Object.values(window.fixtureServer.posts)[0].markdown);
  expect(saved).not.toContain("pending-image:");
  expect(await page.evaluate(() => window.fixtureServer.imagesRegistered)).toBe(2);
  await page.getByRole("button", { name: "削除" }).click();
  await expect.poll(() => body(page).inputValue()).not.toContain("pending-image:");
  await expect(page.getByRole("button", { name: "公開する" })).toBeEnabled();
  expect(await page.evaluate(() => window.fixtureServer.imagesRegistered)).toBe(2);
});

test("dropping an image inserts at the drop point in a scrolled textarea; outside drops are refused", async ({ page }) => {
  await admin(page, {}, 1280, false);
  const lines = Array.from({ length: 120 }, (_, i) => `line ${String(i).padStart(3, "0")}`).join("\n");
  await typeArticle(page, "ドロップ", lines);
  const target = await body(page).evaluate((element: HTMLTextAreaElement) => {
    const style = getComputedStyle(element);
    const lineHeight = parseFloat(style.lineHeight);
    element.scrollTop = lineHeight * 60;
    const rect = element.getBoundingClientRect();
    element.setSelectionRange(0, 0);
    return { x: rect.left + parseFloat(style.paddingLeft) + 200, y: rect.top + parseFloat(style.paddingTop) + lineHeight * 2.5 };
  });
  await page.evaluate(async ({ x, y }) => {
    const source = new Blob([Uint8Array.from(atob("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="), (c) => c.charCodeAt(0))], { type: "image/png" });
    const transfer = new DataTransfer();
    transfer.items.add(new File([source], "drop.png", { type: "image/png" }));
    const area = document.getElementById("markdown")!;
    area.dispatchEvent(new DragEvent("drop", { dataTransfer: transfer, clientX: x, clientY: y, bubbles: true, cancelable: true }));
    const outside = new DataTransfer();
    outside.items.add(new File([source], "outside.png", { type: "image/png" }));
    document.getElementById("title")!.dispatchEvent(new DragEvent("drop", { dataTransfer: outside, bubbles: true, cancelable: true }));
  }, target);
  await expect.poll(() => body(page).inputValue()).toMatch(/line 062\n\n!\[drop\]\(\/api\/images\/\w+\)\n\nline 063/);
  await expect(page.getByText("画像は本文へドロップしてください。")).toBeVisible();
  await expect(title(page)).toHaveValue("ドロップ");
});

test("preview image controls change only the selected occurrence and keep the settings", async ({ page }) => {
  const url = `/api/images/${"i".repeat(43)}`;
  await admin(page, { path: `/admin?view=edit&id=${"b".repeat(43)}`, posts: [{ title: "題", markdown: `![a](${url})\n\n![b](${url})` }] }, 1280, false);
  const frames = page.locator(".preview .blog-image-frame");
  await expect(frames).toHaveCount(2);
  await frames.nth(1).locator("img").click();
  const toolbar = page.getByRole("toolbar", { name: "画像の配置とサイズ" });
  await expect(toolbar).toBeVisible();
  const imageBox = (await frames.nth(1).locator("img").boundingBox())!;
  const toolbarBox = (await toolbar.boundingBox())!;
  expect(toolbarBox.y, "toolbar sits outside the image").toBeGreaterThanOrEqual(imageBox.y + imageBox.height);
  await toolbar.getByRole("button", { name: "中央寄せ" }).click();
  await expect(body(page)).toHaveValue(`![a](${url})\n\n![b](${url} "blog-image:v1;width=auto;align=center")`);
  const handle = page.locator(".image-resize-handle");
  const box = (await handle.boundingBox())!;
  const prose = (await page.locator(".preview .prose").boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x - prose.width * 0.5, box.y + box.height / 2, { steps: 5 });
  await expect(page.locator(".image-size-label")).toContainText("本文幅の");
  await page.mouse.up();
  await expect(page.locator(".image-size-label")).toHaveCount(0);
  await expect.poll(() => body(page).inputValue()).toMatch(/!\[b\]\(\/api\/images\/i+ "blog-image:v1;width=(4[5-9]|5[0-5]);align=center"\)$/);
  const outer = (await page.locator(".preview .blog-image").nth(1).boundingBox())!;
  expect(Math.abs(outer.x + outer.width / 2 - (prose.x + prose.width / 2))).toBeLessThan(3);
  await page.locator(".preview .blog-image-frame").nth(1).locator("img").click();
  await page.getByRole("toolbar").getByRole("button", { name: "サイズを戻す" }).click();
  await expect(body(page)).toHaveValue(`![a](${url})\n\n![b](${url} "blog-image:v1;width=auto;align=center")`);
  await page.getByLabel("タイトル").click();
  await expect(toolbar).toHaveCount(0);
  await expect.poll(() => page.evaluate(() => Object.values(window.fixtureServer.posts)[0].markdown), { timeout: 8_000 }).toContain("align=center");
  // Re-opening shows the saved setting.
  await nav(page).getByRole("link", { name: "記事一覧" }).click();
  await page.getByRole("link", { name: "題" }).click();
  await expect(page.locator(".preview .blog-image").nth(1)).toHaveAttribute("data-align", "center");
  await expect(page.locator(".preview .blog-image").nth(0)).toHaveAttribute("data-align", "left");
});

test("one sync icon requests one source; acceptance is not shown as success", async ({ page }) => {
  await admin(page, { path: "/admin?view=settings", sync: [{ source: "note", status: "failed", errorCode: "upstream_429" }] });
  await expect(page.getByText("noteの同期に失敗しました：媒体側の利用制限に達しました")).toBeVisible();
  await page.getByRole("button", { name: "Qiitaを同期" }).click();
  await expect(page.getByText("Qiitaの同期を待っています…")).toBeVisible();
  await expect(page.getByRole("button", { name: "Qiitaを同期" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Wantedlyを同期" })).toBeEnabled();
  expect(await page.evaluate(() => window.fixtureServer.calls.filter((call) => call.startsWith("POST /api/admin/sync")))).toEqual(["POST /api/admin/sync/qiita"]);
  await page.evaluate(() => {
    Object.assign(window.fixtureServer.sync[0], { status: "partial", coverage: "gap", count: 4, requested: false, lastSuccess: "2026-10-31T00:05:00Z" });
  });
  await page.clock.runFor(3_000);
  await expect(page.getByText(/Qiitaの取得可能な範囲を同期しました.*今回の取得：4件.*取得できていない記事がある可能性があります/)).toBeVisible();
  await page.evaluate(() => {
    window.fixtureServer.sync[1].status = "running";
    window.fixtureServer.fail({ method: "POST", path: "/api/admin/sync/qiita", mode: "status", status: 409, body: { error: "x", reason: "cooldown", retryAfterSeconds: 241 } });
  });
  await page.getByRole("button", { name: "Qiitaを同期" }).click();
  await expect(page.getByText("Qiitaは前回の同期から10分経つまで再実行できません（あと5分）。")).toBeVisible();
  const syncReads = () => page.evaluate(() => window.fixtureServer.calls.filter((call) => call === "GET /api/admin/sync").length);
  const before = await syncReads();
  await page.clock.runFor(3_000);
  expect(await syncReads()).toBeGreaterThan(before);
  await nav(page).getByRole("link", { name: "新規作成" }).click();
  const left = await syncReads();
  await page.clock.runFor(30_000);
  expect(await syncReads(), "polling stops after leaving settings").toBe(left);
});

test("logout lives in settings and clears local recovery copies", async ({ page }) => {
  await admin(page, { path: "/admin?view=settings" });
  await page.getByRole("button", { name: "ログアウト" }).click();
  await expect(page.getByRole("heading", { name: "管理者ログイン" })).toBeVisible();
  expect(await page.evaluate(() => window.fixtureServer.signedIn)).toBe(false);
});

test("signed-out owner can log in", async ({ page }) => {
  await admin(page, { scenario: "signed-out" });
  await page.getByLabel("パスワード").fill("fixture-password");
  await page.getByRole("button", { name: "ログイン" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "新規作成" })).toBeVisible();
});

test("article list shows unpublished changes and opens the latest copy from the title link", async ({ page }) => {
  await admin(page, { posts: [{ title: "公開済みの記事", markdown: "公開版", published: true, publishedAt: "2026-10-01T00:00:00Z" }] });
  await page.evaluate(() => {
    const post = Object.values(window.fixtureServer.posts)[0];
    window.fixtureServer.posts[post.id] = { ...post, markdown: "作業版の変更" };
  });
  await nav(page).getByRole("link", { name: "記事一覧" }).click();
  const row = page.locator(".article-table tbody tr").first();
  await expect(row).toContainText("公開中・未公開の変更あり");
  await row.getByRole("link", { name: "公開済みの記事" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "記事を編集" })).toBeVisible();
  await expect(body(page)).toHaveValue("作業版の変更");
});

test("titles are limited to 200 Unicode code points, not UTF-16 units", async ({ page }) => {
  await admin(page);
  await title(page).fill("😀".repeat(201));
  expect(Array.from(await title(page).inputValue()).length).toBe(200);
});

for (const view of ["articles", "settings"] as const) {
  test(`files dropped on the ${view} screen are refused instead of opened`, async ({ page }) => {
    await admin(page, { path: `/admin?view=${view}` });
    const prevented = await page.evaluate(() => {
      const transfer = new DataTransfer();
      transfer.items.add(new File(["x"], "a.png", { type: "image/png" }));
      const event = new DragEvent("drop", { dataTransfer: transfer, bubbles: true, cancelable: true });
      document.querySelector("main")!.dispatchEvent(event);
      return event.defaultPrevented;
    });
    expect(prevented).toBe(true);
    await expect(page.getByText("画像は本文へドロップしてください。")).toBeVisible();
  });
}

test("sync icons show their description on keyboard focus", async ({ page }) => {
  await admin(page, { path: "/admin?view=settings" });
  const qiita = page.getByRole("button", { name: "Qiitaを同期" });
  await expect(qiita).toBeEnabled();
  const tooltip = qiita.locator(".sync-tooltip");
  await expect(tooltip).toHaveCSS("opacity", "0");
  for (let i = 0; i < 30 && !(await qiita.evaluate((el) => el === document.activeElement)); i++) await page.keyboard.press("Tab");
  await expect(qiita).toBeFocused();
  await expect(tooltip).toHaveCSS("opacity", "1");
  await expect(tooltip).toHaveText("Qiitaを同期");
});

const writeRecovery = (page: Page, record: Record<string, unknown>) =>
  page.evaluate(
    (record) =>
      new Promise<void>((resolve, reject) => {
        const open = indexedDB.open("course-k-blog-admin", 1);
        open.onupgradeneeded = () => open.result.createObjectStore("drafts", { keyPath: "key" });
        open.onsuccess = () => {
          const tx = open.result.transaction("drafts", "readwrite");
          tx.objectStore("drafts").put(record);
          tx.oncomplete = () => {
            open.result.close();
            resolve();
          };
          tx.onerror = () => reject(tx.error);
        };
      }),
    record,
  );
const recoveryKeys = (page: Page) =>
  page.evaluate(
    () =>
      new Promise<string[]>((resolve) => {
        const open = indexedDB.open("course-k-blog-admin", 1);
        open.onsuccess = () => {
          const request = open.result.transaction("drafts").objectStore("drafts").getAllKeys();
          request.onsuccess = () => {
            open.result.close();
            resolve(request.result as string[]);
          };
        };
      }),
  );
const recovery = (articleId: string | null, baseVersion: number | null, markdown: string) => ({
  schema: 1,
  key: "dead-tab:local-1",
  tabSessionId: "dead-tab",
  localId: "local-1",
  articleId,
  creationKey: "k".repeat(43),
  initialPayload: null,
  base: baseVersion === null ? null : { version: baseVersion, doc: { title: "題", markdown: "元" } },
  doc: { title: "題", markdown },
  pendingImages: [],
  updatedAt: Date.now(),
});

test("a conflicting restore keeps the device copy until the owner decides", async ({ page }) => {
  const id = "b".repeat(43);
  await admin(page, { path: "/admin?view=articles", posts: [{ id, title: "題", markdown: "別画面の更新", version: 3 }] }, 1280, false);
  await writeRecovery(page, recovery(id, 1, "端末内の編集"));
  await page.reload();
  await page.getByRole("button", { name: "復元" }).click();
  await expect(page.getByRole("heading", { name: "端末内の編集内容はサーバーより古い版が基になっています" })).toBeVisible();
  await expect(body(page)).toHaveValue("別画面の更新");
  expect(await recoveryKeys(page)).toContain("dead-tab:local-1");
  await page.reload();
  await expect(page.locator(".recovery-prompt")).toContainText("題");
  await page.getByRole("button", { name: "復元" }).click();
  page.once("dialog", (dialog) => void dialog.accept());
  await page.getByRole("button", { name: "端末内の内容で置き換える" }).click();
  await expect(body(page)).toHaveValue("端末内の編集");
  await expect.poll(() => recoveryKeys(page)).not.toContain("dead-tab:local-1");
  await expect.poll(() => page.evaluate(() => Object.values(window.fixtureServer.posts)[0].markdown), { timeout: 8_000 }).toBe("端末内の編集");
});

test("logout deletes this app's device copies before leaving", async ({ page }) => {
  await admin(page, { path: "/admin?view=settings" }, 1280, false);
  await writeRecovery(page, recovery(null, null, "残してはいけない下書き"));
  expect(await recoveryKeys(page)).toEqual(["dead-tab:local-1"]);
  await page.getByRole("button", { name: "ログアウト" }).click();
  await expect(page.getByRole("heading", { name: "管理者ログイン" })).toBeVisible();
  expect(await recoveryKeys(page)).toEqual([]);
});

test("image upload hitting an expired session asks for login and resumes after it", async ({ page }) => {
  await admin(page, {}, 1280, false);
  await typeArticle(page, "題", "本文");
  await expect(saveState(page)).toHaveText("保存済み", { timeout: 8_000 });
  await page.evaluate(() => {
    window.fixtureServer.signedIn = false;
  });
  await pasteImages(page, ["after-expiry.png"]);
  await expect(page.getByRole("heading", { name: "管理者ログイン" })).toBeVisible();
  await page.getByLabel("パスワード").fill("fixture-password");
  await page.getByRole("button", { name: "ログイン" }).click();
  await expect.poll(() => body(page).inputValue()).toMatch(/!\[after-expiry\]\(\/api\/images\/\w+\)/);
});

test("an image that fails while leaving can be retried from the dialog", async ({ page }) => {
  await admin(page, {}, 1280, false);
  await typeArticle(page, "題", "本文");
  await page.evaluate(() => {
    window.fixtureServer.uploadFailures["leave.png"] = "corrupt";
  });
  await pasteImages(page, ["leave.png"]);
  await nav(page).getByRole("link", { name: "設定" }).click();
  const dialog = page.getByRole("alertdialog");
  await expect(dialog).toContainText("送信できていない画像があります");
  await page.evaluate(() => {
    delete window.fixtureServer.uploadFailures["leave.png"];
  });
  // The fixture keeps a failed upload failed; the retry must at least reach it again.
  const before = await page.evaluate(() => window.fixtureServer.calls.filter((call) => call.startsWith("POST /api/admin/image-uploads")).length);
  await dialog.getByRole("button", { name: "再試行" }).click();
  await expect.poll(() => page.evaluate(() => window.fixtureServer.calls.filter((call) => call.startsWith("POST /api/admin/image-uploads")).length)).toBeGreaterThan(before);
});

test("a right-aligned image can be shrunk and grown back with the right-edge handle", async ({ page }) => {
  const url = `/api/images/${"i".repeat(43)}`;
  await admin(page, { path: `/admin?view=edit&id=${"b".repeat(43)}`, posts: [{ title: "題", markdown: `![a](${url} "blog-image:v1;width=60;align=right")` }] }, 1280, false);
  const image = page.locator(".preview .blog-image-frame img");
  await image.click();
  const prose = (await page.locator(".preview .prose").boundingBox())!;
  const width = async () => Number((await body(page).inputValue()).match(/width=(\d+)/)?.[1]);
  const drag = async (toX: (handleX: number) => number) => {
    const box = (await page.locator(".image-resize-handle").boundingBox())!;
    const x = box.x + box.width / 2;
    await page.mouse.move(x, box.y + box.height / 2);
    await page.mouse.down();
    await page.mouse.move(toX(x), box.y + box.height / 2, { steps: 5 });
    await page.mouse.up();
  };
  await drag((x) => x - prose.width * 0.3);
  await expect.poll(width).toBeLessThanOrEqual(33);
  // Growing back moves the handle past the content edge, within the window.
  // Each drag re-applies right alignment, so repeating it grows the image further.
  for (let i = 0; i < 3; i++) {
    const before = await width();
    await image.click();
    await drag(() => 1278);
    await expect.poll(width).toBeGreaterThan(before);
  }
  expect(await width()).toBeGreaterThanOrEqual(55);
  await expect(body(page)).toHaveValue(/align=right/);
  const outer = (await page.locator(".preview .blog-image").boundingBox())!;
  expect(Math.abs(outer.x + outer.width - (prose.x + prose.width)), "right alignment is re-applied").toBeLessThan(3);
});

test("a first-save conflict found after restoring still protects the article after another reload", async ({ page }) => {
  const id = "b".repeat(43);
  const key = "k".repeat(43);
  await admin(
    page,
    {
      path: "/admin?view=articles",
      posts: [{ id, title: "題", markdown: "別画面の更新", version: 7 }],
      creations: { [key]: { postId: id, title: "題", markdown: "元" } },
    },
    1280,
    false,
  );
  await writeRecovery(page, { ...recovery(null, null, "端末内の編集"), initialPayload: { title: "題", markdown: "元" } });
  await page.reload();
  await page.getByRole("button", { name: "復元" }).click();
  await expect(saveState(page)).toContainText("別の画面で更新されています", { timeout: 8_000 });
  // The conflict reaches the device copy (with the article ID) before reloading.
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          new Promise<unknown>((resolve) => {
            const open = indexedDB.open("course-k-blog-admin", 1);
            open.onsuccess = () => {
              const request = open.result.transaction("drafts").objectStore("drafts").getAll();
              request.onsuccess = () => {
                open.result.close();
                resolve((request.result as { articleId: string | null; base: { version: number } | null }[]).map((r) => [r.articleId, r.base?.version ?? null]));
              };
            };
          }),
      ),
    )
    .toEqual([[id, 1]]);
  await page.reload();
  await page.getByRole("button", { name: "復元" }).click();
  await expect(page.getByRole("heading", { name: "端末内の編集内容はサーバーより古い版が基になっています" })).toBeVisible();
  await expect(body(page)).toHaveValue("別画面の更新");
  await page.waitForTimeout(4_000);
  expect(await page.evaluate(() => window.fixtureServer.calls.filter((call) => call.startsWith("PUT /api/admin/posts")))).toEqual([]);
  expect(await page.evaluate(() => Object.values(window.fixtureServer.posts)[0].markdown)).toBe("別画面の更新");
});

test("choosing new again keeps the input; leaving during IME composition waits for the confirmed text", async ({ page }) => {
  await admin(page, {}, 1280, false);
  await title(page).fill("変換");
  await nav(page).getByRole("link", { name: "新規作成" }).click();
  await expect(title(page)).toHaveValue("変換");
  const compose = (type: "compositionstart" | "compositionend") =>
    page.evaluate((type) => document.getElementById("markdown")!.dispatchEvent(new CompositionEvent(type, { bubbles: true, data: "" })), type);
  await compose("compositionstart");
  await body(page).fill("にほんご");
  await nav(page).getByRole("link", { name: "設定" }).click();
  await page.waitForTimeout(4_000);
  expect(await writes(page), "unconfirmed text is not sent").toEqual([]);
  await expect(page.getByRole("heading", { level: 1, name: "新規作成" })).toBeVisible();
  await body(page).fill("日本語");
  await compose("compositionend");
  await expect(page.getByRole("heading", { level: 1, name: "設定" })).toBeVisible();
  expect(await page.evaluate(() => Object.values(window.fixtureServer.posts).map((post) => post.markdown))).toEqual(["日本語"]);
});
