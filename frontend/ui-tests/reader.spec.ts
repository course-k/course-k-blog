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
