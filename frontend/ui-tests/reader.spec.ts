import { test, expect, showScreen } from "./helpers";

for (const width of [1280, 390, 320]) {
  test(`article ${width}: headings, TOC, safe Markdown and overflow`, async ({ page }) => {
    await showScreen(page, { screen: "article" }, width);
    await expect(page.locator("#article-section-4")).toBeVisible();
    await expect(page.locator(".reading-content h2")).toHaveCount(3);
    await expect(page.locator(".reading-content h3")).toHaveCount(1);
    await expect(page.locator(".toc")).not.toContainText("コード内は目次に含めない");
    if (width > 900) {
      const content = await page.locator(".reading-content").boundingBox();
      const toc = await page.locator(".toc").boundingBox();
      expect(content).not.toBeNull();
      expect(toc).not.toBeNull();
      expect(toc!.x).toBeGreaterThan(content!.x + content!.width);
    } else {
      await expect(page.locator(".toc-mobile")).not.toHaveAttribute("open", "");
      await page.locator("summary").press("Enter");
      await expect(page.locator(".toc-mobile")).toHaveAttribute("open", "");
      await page.locator(".toc-mobile a").last().click();
      expect(await page.evaluate(() => location.hash)).toBe("#article-section-4");
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await expect(page.locator(".reading-content img")).toBeVisible();
    expect(await page.locator(".reading-content img").evaluate(image => (image as HTMLImageElement).naturalWidth)).toBe(1200);
  });
}

for (const width of [1280, 390, 320]) {
  test(`list ${width}: layout, external link and keyboard focus`, async ({ page }) => {
    await showScreen(page, { screen: "list" }, width);
    const cards = page.locator(".post-row");
    const first = await cards.nth(0).boundingBox();
    const second = await cards.nth(1).boundingBox();
    expect(first).not.toBeNull();
    expect(second).not.toBeNull();
    expect(second!.x).toBe(first!.x);
    expect(second!.y).toBeGreaterThan(first!.y + first!.height);
    const external = page.locator(".post-row").getByRole("link", { name: /新しいタブで開く/ });
    await expect(external).toHaveAttribute("target", "_blank");
    await expect(external).toHaveAttribute("rel", "noopener noreferrer");
    await cards.first().locator("a").focus();
    await expect(cards.first().locator("a")).toBeFocused();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  });
}

test("empty list is distinguished from failure", async ({ page }) => {
  await showScreen(page, { screen: "list", empty: true });
  await expect(page.getByText("最初の記事を準備しています")).toBeVisible();
});

for (const width of [1280, 390, 320]) {
  test(`profile and monthly archive ${width}: keyboard disclosure and month links`, async ({ page }) => {
    await showScreen(page, { screen: "list" }, width);
    await expect(page.getByRole("heading", { level: 1, name: "course_kのブログ" })).toBeVisible();
    const wordmark = page.getByRole("heading", { level: 1, name: "course_kのブログ" }).getByRole("img");
    await expect(wordmark).toHaveAttribute("src", "/brand/wordmark.png");
    expect(await wordmark.evaluate(img => (img as HTMLImageElement).naturalWidth)).toBe(2027);
    const avatar = page.getByRole("img", { name: "バイクに乗るcourse_k", exact: true });
    await expect(avatar).toHaveAttribute("src", "/profile/rider.webp");
    await avatar.evaluate(img => (img as HTMLImageElement).decode());
    expect(await avatar.evaluate(img => (img as HTMLImageElement).naturalWidth)).toBe(837);
    const brand = page.getByRole("link", { name: "course_kのブログのトップへ", exact: true });
    await expect(brand.locator("img")).toHaveAttribute("src", "/brand/mark.svg");
    const brandBox = (await brand.boundingBox())!;
    const navigation = (await page.getByRole("navigation", { name: "メイン", exact: true }).boundingBox())!;
    expect(navigation.x).toBeGreaterThanOrEqual(brandBox.x + brandBox.width);
    expect(navigation.x - brandBox.x - brandBox.width).toBeLessThanOrEqual(32);
    await brand.focus();
    await expect(brand).toBeFocused();
    const profile = await page.getByRole("region", { name: "プロフィール", exact: true }).boundingBox();
    const article = await page.locator(".post-row").first().boundingBox();
    expect(profile!.y + profile!.height).toBeLessThan(article!.y);
    const archive = page.locator(".month-archive");
    await expect(archive).not.toHaveAttribute("open", "");
    await archive.locator(":scope > summary").press("Enter");
    await expect(archive).toHaveAttribute("open", "");
    await expect(archive.getByRole("link", { name: "10月 (22)", exact: true })).toHaveAttribute("href", "/?month=2026-10");
    await archive.getByText("2025年", { exact: true }).press("Enter");
    await expect(archive.getByRole("link", { name: "12月 (3)", exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  });
}

const imageArticle = [
  "## 画像",
  "前置き".repeat(200),
  `![中央の図](/api/images/${"i".repeat(43)} "blog-image:v1;width=50;align=center")`,
  `![右の小さな印](/apple-touch-icon.png "blog-image:v1;width=100;align=right;title=%E5%8D%B0")`,
  `![通常の説明付き](/api/images/${"i".repeat(43)} "普通の説明")`,
  `![壊れた設定](/api/images/${"i".repeat(43)} "blog-image:v1;width=5;align=left")`,
  "`![コード](/x.png \"blog-image:v1;width=50;align=center\")`",
  "後書き".repeat(400),
].join("\n\n");

for (const width of [1280, 390]) {
  test(`public images ${width}: width share, alignment, no upscaling, safe settings`, async ({ page }) => {
    await showScreen(page, { screen: "article", articleMarkdown: imageArticle }, width);
    const images = page.locator(".reading-content .blog-image");
    await expect(images).toHaveCount(4);
    const prose = (await page.locator(".reading-content .prose, .reading-content").first().boundingBox())!;
    const content = await page.locator(".reading-content").evaluate((element) => element.getBoundingClientRect().width);
    const center = (await images.nth(0).locator("img").boundingBox())!;
    expect(Math.abs(center.width - content * 0.5)).toBeLessThan(2);
    expect(Math.abs(center.x + center.width / 2 - (prose.x + content / 2))).toBeLessThan(2);
    const mark = images.nth(1).locator("img");
    await mark.evaluate((image: HTMLImageElement) => image.decode());
    const markBox = (await mark.boundingBox())!;
    expect(Math.round(markBox.width)).toBe(Math.min(180, Math.round(content)));
    expect(Math.abs(markBox.x + markBox.width - (prose.x + content))).toBeLessThan(2);
    await expect(mark).toHaveAttribute("title", "印");
    await expect(images.nth(2).locator("img")).toHaveAttribute("title", "普通の説明");
    await expect(images.nth(3)).toHaveAttribute("data-align", "left");
    await expect(images.nth(3).locator("img")).not.toHaveAttribute("title", /.*/);
    await expect(page.locator(".reading-content code")).toContainText("blog-image:v1");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  });
}

test("public image enlarges in place and restores focus and scroll when closed", async ({ page }) => {
  await showScreen(page, { screen: "article", articleMarkdown: imageArticle }, 1280);
  const trigger = page.getByRole("button", { name: "画像を拡大表示：中央の図" });
  await trigger.scrollIntoViewIfNeeded();
  const scrolled = await page.evaluate(() => scrollY);
  // The image is the click target; the button around it carries the name and keyboard focus.
  await trigger.getByRole("img").click();
  const dialog = page.getByRole("dialog", { name: "拡大表示：中央の図" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("button", { name: "閉じる" })).toBeFocused();
  const enlarged = (await dialog.locator("img").boundingBox())!;
  expect(enlarged.width).toBeLessThanOrEqual(1280);
  expect(enlarged.width).toBeGreaterThan(600);
  expect(await page.evaluate(() => document.body.style.overflow)).toBe("hidden");
  await dialog.locator("img").click();
  await expect(dialog).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(trigger).toBeFocused();
  expect(await page.evaluate(() => scrollY)).toBe(scrolled);
  await trigger.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.mouse.click(5, 5);
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.getByRole("img", { name: "右の小さな印" }).click();
  const small = (await page.getByRole("dialog").locator("img").boundingBox())!;
  expect(Math.round(small.width)).toBe(180);
  await page.getByRole("button", { name: "閉じる" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
});
