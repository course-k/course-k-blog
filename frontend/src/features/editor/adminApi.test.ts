import test, { afterEach } from "node:test";
import assert from "node:assert/strict";
import { ApiError, ResponseFormatError, requestAdminJson, outcomeIsUnknown } from "./adminApi.ts";

const originalFetch = globalThis.fetch;
afterEach(() => { globalThis.fetch = originalFetch; });

for (const status of [400, 401, 403, 408, 409, 429, 500, 503]) {
  test(`non-JSON HTTP ${status} retains its status and outcome classification`, async () => {
    globalThis.fetch = async () => new Response("upstream response", { status });
    await assert.rejects(requestAdminJson("/session"), error => {
      assert(error instanceof ApiError);
      assert.equal(error.status, status);
      assert.equal(outcomeIsUnknown(error), status >= 500 || status === 408);
      return true;
    });
  });
}

test("successful malformed JSON has an unknown write outcome", async () => {
  globalThis.fetch = async () => new Response("not JSON", { status: 200 });
  await assert.rejects(requestAdminJson("/admin/posts", "POST", {}), error => {
    assert(error instanceof ResponseFormatError);
    assert(outcomeIsUnknown(error));
    return true;
  });
});

test("JSON API rejection preserves the server message", async () => {
  globalThis.fetch = async () => new Response(JSON.stringify({ error: "fixture rejection" }), { status: 409 });
  await assert.rejects(requestAdminJson("/admin/posts/example", "PUT", {}), error => {
    assert(error instanceof ApiError);
    assert.equal(error.message, "fixture rejection");
    return true;
  });
});

test("request deadline aborts pending transport without asserting that a write failed", async () => {
  const controller = new AbortController();
  globalThis.fetch = async (_input, init) => new Promise((_resolve, reject) => {
    init?.signal?.addEventListener("abort", () => reject(init.signal?.reason), { once: true });
  });
  const timer = setTimeout(() => controller.abort(new Error("fixture deadline")), 5);
  try {
    await assert.rejects(requestAdminJson("/admin/posts", "POST", {}, controller.signal), error => {
      assert(outcomeIsUnknown(error));
      return true;
    });
  } finally { clearTimeout(timer); }
});

test("transport sends same-origin JSON credentials and an abort signal", async () => {
  globalThis.fetch = async (input, init) => {
    assert.equal(input, "/api/admin/posts");
    assert.equal(init?.method, "POST");
    assert.equal(init?.credentials, "same-origin");
    assert.equal(init?.cache, "no-store");
    assert(init?.signal instanceof AbortSignal);
    assert.deepEqual(JSON.parse(String(init?.body)), { title: "title" });
    return new Response(JSON.stringify({ id: "fixture" }));
  };
  assert.deepEqual(await requestAdminJson("/admin/posts", "POST", { title: "title" }), { id: "fixture" });
});
