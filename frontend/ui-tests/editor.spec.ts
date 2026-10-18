import {
  test,
  expect,
  showScreen,
  startArticle,
  publishArticle,
  loseNextResponse,
} from "./helpers";

test("saved article can be published, previewed on mobile, hidden and logged out", async ({
  page,
}) => {
  await showScreen(page, { screen: "admin" });
  await startArticle(page);
  const input = await page.locator(".markdown-input").boundingBox();
  const preview = await page.locator(".preview").boundingBox();
  expect(preview!.x).toBeGreaterThan(input!.x);
  await page.getByRole("button", { name: "下書きを保存", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("下書きを保存しました");
  await publishArticle(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator(".preview")).toBeHidden();
  await page.getByRole("button", { name: "プレビュー", exact: true }).click();
  await expect(
    page
      .getByRole("region", { name: "プレビュー" })
      .getByRole("heading", { name: "確認する見出し", exact: true }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "非公開に戻す", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("非公開にしました");
  await expect(page.locator(".editor-state")).toContainText("下書き");
  await page.getByRole("button", { name: "ログアウト", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "管理者ログイン" }),
  ).toBeVisible();
});

for (const action of ["navigate", "new", "logout"] as const) {
  test(`unsaved changes: cancelling ${action} preserves input`, async ({
    page,
  }) => {
    await showScreen(page, { screen: "admin" });
    await startArticle(page);
    let confirmed = false;
    page.once("dialog", async (dialog) => {
      confirmed = true;
      await dialog.dismiss();
    });
    if (action === "navigate")
      await page.evaluate(() => {
        void window.navigateAway();
      });
    else
      await page
        .getByRole("button", {
          name: action === "new" ? "＋ 新しい記事" : "ログアウト",
          exact: true,
        })
        .click();
    await expect.poll(() => confirmed).toBe(true);
    await expect(page.getByLabel("タイトル", { exact: true })).toHaveValue(
      "検証用記事",
    );
    expect(
      await page.evaluate(
        () =>
          window.calls.filter((call) => call === "DELETE /api/session").length,
      ),
    ).toBe(0);
  });
}

test("list refresh failure does not turn a successful save into a failed write", async ({
  page,
}) => {
  await showScreen(page, { screen: "admin", scenario: "list-failure" });
  await startArticle(page);
  await page.getByRole("button", { name: "下書きを保存", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("下書きを保存しました");
  await expect(page.getByRole("alert")).toContainText(
    "記事一覧を更新できません",
  );
  await expect(
    page.getByRole("button", { name: "下書きを保存", exact: true }),
  ).toBeEnabled();
});

test("publish server error preserves saved input and requires outcome confirmation", async ({
  page,
}) => {
  await showScreen(page, { screen: "admin", scenario: "publish-failure" });
  await startArticle(page);
  await page.getByRole("button", { name: "公開する", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("下書きは保存済み");
  await expect(page.getByRole("alert")).toContainText(
    "公開結果を確認できません",
  );
  await expect(
    page.getByRole("button", { name: "公開する", exact: true }),
  ).toBeDisabled();
});

test("lost create response is resolved by its creation key without another POST", async ({
  page,
}) => {
  await showScreen(page, { screen: "admin", scenario: "lost-response" });
  await startArticle(page);
  await page.getByRole("button", { name: "下書きを保存", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "保存結果を確認", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "保存結果を確認", exact: true })
    .click();
  await expect(page.getByRole("status")).toContainText("状態を確認しました");
  await expect(
    page.getByRole("button", { name: "公開する", exact: true }),
  ).toBeEnabled();
  expect(
    await page.evaluate(
      () =>
        window.calls.filter((call) => call === "POST /api/admin/posts").length,
    ),
  ).toBe(1);
  await expect(page.getByLabel("タイトル", { exact: true })).toHaveValue(
    "検証用記事",
  );
});

test("existing article PUT conflict retains unsaved input", async ({
  page,
}) => {
  await showScreen(page, { screen: "admin", scenario: "conflict" });
  await startArticle(page);
  await page.getByRole("button", { name: "下書きを保存", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("下書きを保存しました");
  await page.getByLabel("タイトル", { exact: true }).fill("競合中の入力");
  await page.getByRole("button", { name: "下書きを保存", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("競合しました");
  await expect(page.getByLabel("タイトル", { exact: true })).toHaveValue(
    "競合中の入力",
  );
  expect(
    await page.evaluate(() =>
      window.calls.some((call) => call.startsWith("PUT /api/admin/posts/")),
    ),
  ).toBe(true);
});

for (const action of ["reselect", "new", "logout", "navigate"] as const) {
  test(`lost unpublish response: cancelling ${action} preserves the unknown outcome`, async ({
    page,
  }) => {
    await showScreen(page, { screen: "admin" });
    await startArticle(page);
    await publishArticle(page);
    await loseNextResponse(page, "/unpublish");
    page.once("dialog", (dialog) => dialog.accept());
    await page
      .getByRole("button", { name: "非公開に戻す", exact: true })
      .click();
    await expect(
      page.getByRole("button", { name: "保存結果を確認", exact: true }),
    ).toBeVisible();
    let confirmed = false;
    page.once("dialog", async (dialog) => {
      confirmed = true;
      await dialog.dismiss();
    });
    if (action === "reselect")
      await page.locator(".draft-list button").first().click();
    else if (action === "navigate")
      await page.evaluate(() => {
        void window.navigateAway();
      });
    else
      await page
        .getByRole("button", {
          name: action === "new" ? "＋ 新しい記事" : "ログアウト",
          exact: true,
        })
        .click();
    await expect.poll(() => confirmed).toBe(true);
    await expect(
      page.getByRole("button", { name: "保存結果を確認", exact: true }),
    ).toBeVisible();
    await expect(page.getByLabel("Markdown", { exact: true })).toHaveValue(
      "## 確認する見出し\n\n保存する本文。",
    );
  });
}

test("confirmed reselect fetches latest visibility instead of reopening stale list data", async ({
  page,
}) => {
  await showScreen(page, { screen: "admin" });
  await startArticle(page);
  await publishArticle(page);
  await loseNextResponse(page, "/unpublish");
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "非公開に戻す", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "保存結果を確認", exact: true }),
  ).toBeVisible();
  page.once("dialog", (dialog) => dialog.accept());
  await page.locator(".draft-list button").first().click();
  await expect(page.locator(".editor-state")).toContainText("下書き");
  await expect(
    page.getByRole("button", { name: "保存結果を確認", exact: true }),
  ).toHaveCount(0);
});

test("lost publish response: confirmation uses ID and preserves later input", async ({
  page,
}) => {
  await showScreen(page, { screen: "admin" });
  await startArticle(page);
  await loseNextResponse(page, "/publish");
  await page.getByRole("button", { name: "公開する", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "保存結果を確認", exact: true }),
  ).toBeVisible();
  await page
    .getByLabel("タイトル", { exact: true })
    .fill("確認前に追記した入力");
  await page
    .getByRole("button", { name: "保存結果を確認", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "更新を公開する", exact: true }),
  ).toBeEnabled();
  await expect(page.getByLabel("タイトル", { exact: true })).toHaveValue(
    "確認前に追記した入力",
  );
});

test("failed outcome check keeps the original unknown state", async ({
  page,
}) => {
  await showScreen(page, { screen: "admin" });
  await startArticle(page);
  await loseNextResponse(page, "/publish");
  await page.getByRole("button", { name: "公開する", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "保存結果を確認", exact: true }),
  ).toBeVisible();
  await page.evaluate(() => {
    const original = window.fetch;
    window.fetch = async (...args) =>
      args[1]?.method === "GET" &&
      String(args[0]).startsWith("/api/admin/posts/")
        ? new Response("upstream failed", { status: 503 })
        : original(...args);
  });
  await page
    .getByRole("button", { name: "保存結果を確認", exact: true })
    .click();
  await expect(page.getByRole("alert")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "保存結果を確認", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "公開する", exact: true }),
  ).toBeDisabled();
});

test("saving unchanged published text does not mark unpublished changes", async ({
  page,
}) => {
  await showScreen(page, { screen: "admin" });
  await startArticle(page);
  await publishArticle(page);
  await page.getByRole("button", { name: "下書きを保存", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("下書きを保存しました");
  await expect(page.locator(".editor-state")).toHaveText("公開中");
});

test("signed-out owner can log in", async ({ page }) => {
  await showScreen(page, { screen: "admin", scenario: "signed-out" });
  await page
    .getByLabel("パスワード", { exact: true })
    .fill("fixture-only-password");
  await page.getByRole("button", { name: "ログイン", exact: true }).click();
  await expect(page.getByRole("heading", { name: "記事を書く" })).toBeVisible();
});

test("session expiry during confirmation preserves unknown outcome through login", async ({
  page,
}) => {
  await showScreen(page, { screen: "admin" });
  await startArticle(page);
  await loseNextResponse(page, "/publish");
  await page.getByRole("button", { name: "公開する", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "保存結果を確認", exact: true }),
  ).toBeVisible();
  await page.evaluate(() => {
    const original = window.fetch;
    window.fetch = async (...args) =>
      args[1]?.method === "GET" &&
      String(args[0]).startsWith("/api/admin/posts/")
        ? new Response(JSON.stringify({ error: "login required" }), {
            status: 401,
          })
        : original(...args);
  });
  await page
    .getByRole("button", { name: "保存結果を確認", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "管理者ログイン" }),
  ).toBeVisible();
  await page.getByLabel("パスワード", { exact: true }).fill("fixture-password");
  await page.getByRole("button", { name: "ログイン", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "保存結果を確認", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "公開する", exact: true }),
  ).toBeDisabled();
  await expect(page.getByLabel("タイトル", { exact: true })).toHaveValue(
    "検証用記事",
  );
});

test("managed re-login preserves the editor and its unknown outcome", async ({
  page,
}) => {
  await showScreen(page, { screen: "admin", oidc: true });
  await startArticle(page);
  await loseNextResponse(page, "/publish");
  await page.getByRole("button", { name: "公開する", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "保存結果を確認", exact: true }),
  ).toBeVisible();
  await page.evaluate(() => {
    const original = window.fetch;
    window.open = () => ({ close() {} }) as Window;
    window.fetch = async (...args) =>
      args[1]?.method === "GET" &&
      String(args[0]).startsWith("/api/admin/posts/")
        ? new Response(JSON.stringify({ error: "login required" }), {
            status: 401,
          })
        : original(...args);
  });
  await page
    .getByRole("button", { name: "保存結果を確認", exact: true })
    .click();
  await page
    .getByRole("button", { name: "専用画面でログイン", exact: true })
    .click();
  await expect(page.getByLabel("タイトル", { exact: true })).toHaveValue(
    "検証用記事",
  );
  await expect(
    page.getByRole("button", { name: "保存結果を確認", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "公開する", exact: true }),
  ).toBeDisabled();
});

test("external corrections guard unsaved navigation and keep hidden/title changes after refresh", async ({
  page,
}) => {
  await showScreen(page, { screen: "admin", external: true });
  const panel = page.getByRole("region", { name: "外部の記事", exact: true });
  await expect(panel).toContainText("最新の取得可能な範囲");
  await panel
    .getByLabel("補正する外部記事")
    .selectOption("qiita:fixture-external");
  await panel
    .getByLabel("タイトルの補正", { exact: false })
    .fill("自分で補正したタイトル");
  await panel.getByLabel("一覧に表示しない", { exact: true }).check();
  page.once("dialog", (dialog) => dialog.dismiss());
  await page.getByRole("button", { name: "ログアウト", exact: true }).click();
  await expect(
    panel.getByLabel("タイトルの補正", { exact: false }),
  ).toHaveValue("自分で補正したタイトル");
  await panel
    .getByRole("button", { name: "外部記事の設定を保存", exact: true })
    .click();
  await panel
    .getByRole("button", { name: "外部記事の状態を再取得", exact: true })
    .click();
  await panel
    .getByLabel("補正する外部記事")
    .selectOption("qiita:fixture-external");
  await expect(
    panel.getByLabel("一覧に表示しない", { exact: true }),
  ).toBeChecked();
  await expect(
    panel.getByLabel("タイトルの補正", { exact: false }),
  ).toHaveValue("自分で補正したタイトル");
  await panel.getByRole("button", { name: "Qiitaを同期", exact: true }).click();
  expect(
    await page.evaluate(
      () =>
        window.calls.filter(
          (call) =>
            call === "PATCH /api/admin/external-posts/qiita/fixture-external",
        ).length,
    ),
  ).toBe(1);
});

test("external correction survives session expiry and re-login", async ({
  page,
}) => {
  await showScreen(page, { screen: "admin", external: true });
  const panel = page.getByRole("region", { name: "外部の記事", exact: true });
  await panel
    .getByLabel("補正する外部記事")
    .selectOption("qiita:fixture-external");
  await panel
    .getByLabel("タイトルの補正", { exact: false })
    .fill("失効しても保持する補正");
  await page.evaluate(() => {
    const original = window.fetch;
    let expired = false;
    window.fetch = async (...args) => {
      if (
        !expired &&
        String(args[0]).includes("/api/admin/external-posts/") &&
        args[1]?.method === "PATCH"
      ) {
        expired = true;
        return new Response(JSON.stringify({ error: "login required" }), {
          status: 401,
        });
      }
      return original(...args);
    };
  });
  await panel
    .getByRole("button", { name: "外部記事の設定を保存", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "管理者ログイン", exact: true }),
  ).toBeVisible();
  await page.getByLabel("パスワード", { exact: true }).fill("fixture-password");
  await page.getByRole("button", { name: "ログイン", exact: true }).click();
  await expect(
    panel.getByLabel("タイトルの補正", { exact: false }),
  ).toHaveValue("失効しても保持する補正");
});

test("unknown external correction remains guarded even after returning input to its original value", async ({
  page,
}) => {
  await showScreen(page, { screen: "admin", external: true });
  const panel = page.getByRole("region", { name: "外部の記事", exact: true });
  await panel
    .getByLabel("補正する外部記事")
    .selectOption("qiita:fixture-external");
  await panel
    .getByLabel("タイトルの補正", { exact: false })
    .fill("応答を失う補正");
  await loseNextResponse(page, "/fixture-external");
  await panel
    .getByRole("button", { name: "外部記事の設定を保存", exact: true })
    .click();
  await expect(
    panel.getByRole("button", {
      name: "外部記事の保存結果を確認",
      exact: true,
    }),
  ).toBeVisible();
  await panel.getByLabel("タイトルの補正", { exact: false }).fill("");
  page.once("dialog", (dialog) => dialog.dismiss());
  await page.getByRole("button", { name: "ログアウト", exact: true }).click();
  await expect(
    panel.getByRole("button", {
      name: "外部記事の保存結果を確認",
      exact: true,
    }),
  ).toBeVisible();
  await panel
    .getByRole("button", { name: "外部記事の保存結果を確認", exact: true })
    .click();
  await expect(
    panel.getByLabel("タイトルの補正", { exact: false }),
  ).toHaveValue("");
  await expect(
    panel.getByRole("button", { name: "外部記事の設定を保存", exact: true }),
  ).toBeEnabled();
  expect(
    await page.evaluate(
      () =>
        window.calls.filter(
          (call) =>
            call === "PATCH /api/admin/external-posts/qiita/fixture-external",
        ).length,
    ),
  ).toBe(1);
});
