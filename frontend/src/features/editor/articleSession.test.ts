import test from "node:test";
import assert from "node:assert/strict";
import { ArticleSession, type Clock, type Doc, type SessionApi } from "./articleSession.ts";
import { ApiError } from "./adminApi.ts";
import type { AdminPost } from "../../lib/types";

class FakeClock implements Clock {
  time = 0;
  private timers = new Map<number, { at: number; callback: () => void }>();
  private next = 1;
  now = () => this.time;
  setTimeout = (callback: () => void, ms: number) => {
    const id = this.next++;
    this.timers.set(id, { at: this.time + ms, callback });
    return id;
  };
  clearTimeout = (handle: unknown) => {
    this.timers.delete(handle as number);
  };
  /** Advances time, running due timers in order and letting promises settle. */
  async advance(ms: number) {
    const target = this.time + ms;
    for (;;) {
      await settle();
      const due = [...this.timers.entries()].filter(([, t]) => t.at <= target).sort((a, b) => a[1].at - b[1].at)[0];
      if (!due) break;
      this.timers.delete(due[0]);
      this.time = due[1].at;
      due[1].callback();
    }
    this.time = target;
    await settle();
  }
}
const settle = async () => {
  for (let i = 0; i < 20; i++) await Promise.resolve();
};

type Call = { method: string; args: unknown[] };
type Responder = (call: Call) => Promise<AdminPost> | AdminPost;

class FakeServer implements SessionApi {
  calls: Call[] = [];
  post: AdminPost | null = null;
  creations = new Map<string, string>();
  responders: Responder[] = [];
  private respond(call: Call, fallback: () => AdminPost): Promise<AdminPost> {
    this.calls.push(call);
    const responder = this.responders.shift();
    return Promise.resolve(responder ? responder(call) : fallback());
  }
  private store(doc: Doc, id = "post-1") {
    this.post = {
      id,
      title: doc.title,
      markdown: doc.markdown,
      version: (this.post?.version ?? 0) + 1,
      published: this.post?.published ?? false,
      publishedAt: this.post?.publishedAt ?? null,
      updatedAt: new Date(0).toISOString(),
      hasUnpublishedChanges: true,
    };
    return { ...this.post };
  }
  create = (doc: Doc, key: string) =>
    this.respond({ method: "create", args: [doc, key] }, () => {
      this.creations.set(key, "post-1");
      return this.store(doc);
    });
  save = (id: string, doc: Doc, version: number) =>
    this.respond({ method: "save", args: [id, doc, version] }, () => {
      if (this.post!.version !== version) throw new ApiError(409, "conflict");
      return this.store(doc, id);
    });
  get = (id: string) =>
    this.respond({ method: "get", args: [id] }, () => {
      if (!this.post) throw new ApiError(404, "not found");
      return { ...this.post };
    });
  findCreation = (key: string) =>
    this.respond({ method: "findCreation", args: [key] }, () => {
      if (!this.creations.has(key)) throw new ApiError(404, "not found");
      return { ...this.post! };
    });
  publish = (id: string, version: number) =>
    this.respond({ method: "publish", args: [id, version] }, () => {
      if (this.post!.version !== version) throw new ApiError(409, "conflict");
      this.post = { ...this.post!, published: true, publishedAt: "2026-10-31T00:00:00Z", version: version + 1, hasUnpublishedChanges: false };
      return { ...this.post };
    });
  unpublish = (id: string, version: number) =>
    this.respond({ method: "unpublish", args: [id, version] }, () => {
      this.post = { ...this.post!, published: false, version: version + 1 };
      return { ...this.post };
    });
  writes() {
    return this.calls.filter((c) => c.method === "create" || c.method === "save");
  }
}

function setup(init = {}) {
  const clock = new FakeClock();
  const server = new FakeServer();
  const created: string[] = [];
  const session = new ArticleSession(server, init, { onCreated: (post) => created.push(post.id) }, clock);
  return { clock, server, session, created };
}

test("an empty new article sends nothing", async () => {
  const { clock, server, session } = setup();
  await clock.advance(60_000);
  assert.equal(server.calls.length, 0);
  assert.equal(session.getSnapshot().save, "none");
});

test("saves 3 seconds after the last edit, not at 2.9 seconds", async () => {
  const { clock, server, session, created } = setup();
  session.setDocument({ title: "", markdown: "本文" });
  await clock.advance(2_900);
  assert.equal(server.writes().length, 0);
  assert.equal(session.getSnapshot().save, "dirty");
  await clock.advance(100);
  assert.equal(server.writes().length, 1);
  assert.equal(server.calls[0].method, "create");
  assert.deepEqual(created, ["post-1"]);
  assert.equal(session.getSnapshot().save, "saved");
});

test("continuous typing saves at 30 seconds without extending the deadline", async () => {
  const { clock, server, session } = setup();
  for (let t = 0; t < 29_900; t += 1_000) {
    session.setDocument({ title: "", markdown: "x".repeat(t / 1000 + 1) });
    await clock.advance(1_000 - (t === 29_000 ? 100 : 0));
  }
  assert.equal(clock.now(), 29_900);
  assert.equal(server.writes().length, 0, "no save before 30 s");
  await clock.advance(100);
  assert.equal(server.writes().length, 1, "saved at 30 s");
  // The next 30 s window starts from the first edit after that snapshot.
  for (let i = 0; i < 29; i++) {
    session.setDocument({ title: "", markdown: "y".repeat(i + 1) });
    await clock.advance(1_000);
  }
  assert.equal(server.writes().length, 1);
  session.setDocument({ title: "", markdown: "z" });
  await clock.advance(1_000);
  assert.equal(server.writes().length, 2);
});

test("unchanged content is not written again and undo clears dirtiness", async () => {
  const { clock, server, session } = setup();
  session.setDocument({ title: "t", markdown: "a" });
  await clock.advance(3_000);
  assert.equal(server.writes().length, 1);
  session.setDocument({ title: "t", markdown: "ab" });
  session.setDocument({ title: "t", markdown: "a" });
  assert.equal(session.getSnapshot().save, "saved");
  await clock.advance(60_000);
  assert.equal(server.writes().length, 1);
});

test("edits during a slow save stay unsaved and are sent after the response", async () => {
  const { clock, server, session } = setup({ post: { id: "post-1", title: "t", markdown: "a", version: 1, published: false, publishedAt: null, updatedAt: "", hasUnpublishedChanges: true } });
  server.post = { id: "post-1", title: "t", markdown: "a", version: 1, published: false, publishedAt: null, updatedAt: "", hasUnpublishedChanges: true };
  let release!: () => void;
  server.responders.push(
    (call) =>
      new Promise((resolve) => {
        release = () => {
          server.post = { ...server.post!, markdown: (call.args[1] as Doc).markdown, version: 2 };
          resolve({ ...server.post });
        };
      }),
  );
  session.setDocument({ title: "t", markdown: "ab" });
  await clock.advance(3_000);
  assert.equal(session.getSnapshot().save, "saving");
  session.setDocument({ title: "t", markdown: "abc" });
  await clock.advance(31_000);
  assert.equal(server.writes().length, 1, "one request at a time");
  release();
  await clock.advance(0);
  // 30 s already passed since the newer edit: it is saved right after the response.
  assert.equal(server.writes().length, 2);
  assert.deepEqual(server.writes()[1].args, ["post-1", { title: "t", markdown: "abc" }, 2]);
  assert.equal(session.getSnapshot().doc.markdown, "abc", "an old response never replaces input");
  assert.equal(session.getSnapshot().save, "saved");
});

test("lost creation response is resolved through the creation key without duplicates", async () => {
  const { clock, server, session, created } = setup();
  server.responders.push((call) => {
    server.creations.set(call.args[1] as string, "post-1");
    server.post = { id: "post-1", title: "", markdown: "first", version: 1, published: false, publishedAt: null, updatedAt: "", hasUnpublishedChanges: true };
    throw new TypeError("response lost");
  });
  session.setDocument({ title: "", markdown: "first" });
  await clock.advance(3_000);
  session.setDocument({ title: "", markdown: "first and more" });
  await clock.advance(0);
  assert.equal(server.calls.filter((c) => c.method === "create").length, 1);
  assert.equal(server.calls.some((c) => c.method === "findCreation"), true);
  assert.deepEqual(created, ["post-1"]);
  await clock.advance(3_000);
  assert.equal(server.calls.filter((c) => c.method === "create").length, 1, "no second article");
  assert.deepEqual(server.writes().at(-1)!.args, ["post-1", { title: "", markdown: "first and more" }, 1]);
});

test("creation that never committed is resent with the same key and first payload", async () => {
  const { clock, server, session } = setup();
  server.responders.push(() => {
    throw new ApiError(503, "unavailable");
  });
  session.setDocument({ title: "", markdown: "first" });
  await clock.advance(3_000);
  session.setDocument({ title: "", markdown: "first plus edits" });
  await clock.advance(0);
  assert.equal(session.getSnapshot().save, "retrying");
  await clock.advance(5_000);
  const creates = server.calls.filter((c) => c.method === "create");
  assert.equal(creates.length, 2);
  assert.deepEqual(creates[0].args, creates[1].args, "same key and same payload");
  await clock.advance(3_000);
  assert.deepEqual(server.writes().at(-1)!.args[1], { title: "", markdown: "first plus edits" });
});

test("lost save response that committed is adopted; one that did not is retried with the same update", async () => {
  const base: AdminPost = { id: "post-1", title: "t", markdown: "a", version: 1, published: false, publishedAt: null, updatedAt: "", hasUnpublishedChanges: true };
  {
    const { clock, server, session } = setup({ post: base });
    server.post = { ...base };
    server.responders.push(() => {
      server.post = { ...server.post!, markdown: "ab", version: 2 };
      throw new TypeError("lost");
    });
    session.setDocument({ title: "t", markdown: "ab" });
    await clock.advance(3_000);
    assert.equal(session.getSnapshot().save, "saved");
    assert.equal(session.getSnapshot().version, 2);
    assert.equal(server.writes().length, 1);
  }
  {
    const { clock, server, session } = setup({ post: base });
    server.post = { ...base };
    server.responders.push(() => {
      throw new ApiError(500, "failed before commit");
    });
    session.setDocument({ title: "t", markdown: "ab" });
    await clock.advance(3_000);
    session.setDocument({ title: "t", markdown: "abc" });
    await clock.advance(5_000);
    assert.deepEqual(server.writes()[1].args, ["post-1", { title: "t", markdown: "ab" }, 1], "same update first");
    await clock.advance(3_000);
    assert.deepEqual(server.writes()[2].args, ["post-1", { title: "t", markdown: "abc" }, 2]);
  }
});

test("unparseable success, failed verification and backoff keep the outcome unknown", async () => {
  const base: AdminPost = { id: "post-1", title: "t", markdown: "a", version: 1, published: false, publishedAt: null, updatedAt: "", hasUnpublishedChanges: true };
  const { clock, server, session } = setup({ post: base });
  server.post = { ...base };
  server.responders.push(() => {
    server.post = { ...server.post!, markdown: "ab", version: 2 };
    throw new (class extends Error {})("unparseable");
  });
  server.responders.push(() => {
    throw new TypeError("verification lost");
  });
  session.setDocument({ title: "t", markdown: "ab" });
  await clock.advance(3_000);
  assert.equal(session.getSnapshot().save, "retrying");
  await clock.advance(4_999);
  assert.equal(server.calls.filter((c) => c.method === "get").length, 1);
  await clock.advance(1);
  assert.equal(server.calls.filter((c) => c.method === "get").length, 2);
  assert.equal(session.getSnapshot().save, "saved");
  assert.equal(server.writes().length, 1, "no duplicate write");
});

test("409 stops autosave without overwriting; local overwrite is explicit", async () => {
  const base: AdminPost = { id: "post-1", title: "t", markdown: "a", version: 1, published: false, publishedAt: null, updatedAt: "", hasUnpublishedChanges: true };
  const { clock, server, session } = setup({ post: base });
  server.post = { ...base, markdown: "other tab", version: 5 };
  session.setDocument({ title: "t", markdown: "mine" });
  await clock.advance(3_000);
  assert.equal(session.getSnapshot().save, "conflict");
  assert.equal(session.getSnapshot().doc.markdown, "mine");
  assert.equal(session.getSnapshot().conflictServer?.markdown, "other tab");
  session.setDocument({ title: "t", markdown: "mine 2" });
  await clock.advance(120_000);
  assert.equal(server.writes().length, 1, "no automatic resend after conflict");
  session.overwriteWithLocal();
  await clock.advance(0);
  assert.deepEqual(server.writes().at(-1)!.args, ["post-1", { title: "t", markdown: "mine 2" }, 5]);
  assert.equal(server.post!.markdown, "mine 2");
});

test("401 keeps input and stops sending until login resumes", async () => {
  const base: AdminPost = { id: "post-1", title: "t", markdown: "a", version: 1, published: false, publishedAt: null, updatedAt: "", hasUnpublishedChanges: true };
  const clock = new FakeClock();
  const server = new FakeServer();
  server.post = { ...base };
  let expired = 0;
  const session = new ArticleSession(server, { post: base }, { onAuthExpired: () => expired++ }, clock);
  server.responders.push(() => {
    throw new ApiError(401, "login required");
  });
  session.setDocument({ title: "t", markdown: "ab" });
  await clock.advance(3_000);
  assert.equal(session.getSnapshot().save, "auth");
  assert.equal(expired, 1);
  session.setDocument({ title: "t", markdown: "abc" });
  await clock.advance(120_000);
  assert.equal(server.writes().length, 1);
  session.resumeAfterLogin();
  await clock.advance(0);
  assert.equal(server.post!.markdown, "abc");
});

test("429 waits for Retry-After; 400 does not retry until edited", async () => {
  const base: AdminPost = { id: "post-1", title: "t", markdown: "a", version: 1, published: false, publishedAt: null, updatedAt: "", hasUnpublishedChanges: true };
  const { clock, server, session } = setup({ post: base });
  server.post = { ...base };
  server.responders.push(() => {
    throw new ApiError(429, "slow down", 20);
  });
  session.setDocument({ title: "t", markdown: "ab" });
  await clock.advance(3_000);
  await clock.advance(19_000);
  assert.equal(server.writes().length, 1);
  await clock.advance(1_000);
  assert.equal(server.writes().length, 2);
  server.responders.push(() => {
    throw new ApiError(400, "invalid image setting");
  });
  session.setDocument({ title: "t", markdown: "abc" });
  await clock.advance(3_000);
  assert.equal(session.getSnapshot().save, "invalid");
  assert.equal(session.getSnapshot().message, "invalid image setting");
  await clock.advance(120_000);
  assert.equal(server.writes().length, 3);
  session.setDocument({ title: "t", markdown: "abcd" });
  await clock.advance(3_000);
  assert.equal(server.writes().length, 4);
});

test("flush saves immediately and reports failure without discarding input", async () => {
  const { clock, server, session } = setup();
  session.setDocument({ title: "", markdown: "draft" });
  const saved = session.flush();
  await clock.advance(0);
  assert.equal(await saved, true);
  assert.equal(server.writes().length, 1);
  server.responders.push(() => {
    throw new ApiError(400, "bad");
  });
  session.setDocument({ title: "", markdown: "draft 2" });
  const failed = session.flush();
  await clock.advance(0);
  assert.equal(await failed, false);
  assert.equal(session.getSnapshot().doc.markdown, "draft 2");
});

test("publish saves the latest working copy first and never repeats after an unknown outcome", async () => {
  const { clock, server, session } = setup();
  session.setDocument({ title: "題", markdown: "本文" });
  const published = session.publish();
  await clock.advance(0);
  await published;
  assert.deepEqual(server.calls.map((c) => c.method), ["create", "publish"]);
  assert.deepEqual(server.calls[1].args, ["post-1", 1]);
  assert.equal(session.getSnapshot().published, true);
  // Autosave after publication only changes the working copy.
  session.setDocument({ title: "題", markdown: "本文 改" });
  await clock.advance(3_000);
  assert.equal(server.calls.at(-1)!.method, "save");
  server.responders.push(() => {
    server.post = { ...server.post!, version: server.post!.version + 1, hasUnpublishedChanges: false };
    throw new TypeError("lost");
  });
  await assert.rejects(session.publish(), /確認できません/);
  await clock.advance(0);
  assert.equal(server.calls.filter((c) => c.method === "publish").length, 2);
  assert.match(session.getSnapshot().operationMessage, /公開中/);
  await clock.advance(120_000);
  assert.equal(server.calls.filter((c) => c.method === "publish").length, 2, "no automatic republish");
  await assert.rejects(new ArticleSession(server, {}, {}, clock).publish(), /タイトル/);
});

test("IME composition defers sending and the 30 s limit applies at composition end", async () => {
  const { clock, server, session } = setup();
  session.setComposing(true);
  session.setDocument({ title: "", markdown: "にほ" });
  await clock.advance(31_000);
  assert.equal(server.writes().length, 0);
  session.setDocument({ title: "", markdown: "日本" });
  session.setComposing(false);
  await clock.advance(0);
  assert.equal(server.writes().length, 1);
  assert.deepEqual(server.writes()[0].args[0], { title: "", markdown: "日本" });
});

test("pending image slots are never sent and do not by themselves create an article", async () => {
  const { clock, server, session } = setup();
  session.setDocument({ title: "", markdown: "![a.png](pending-image:abc)" });
  await clock.advance(60_000);
  assert.equal(server.writes().length, 0);
  session.setDocument({ title: "", markdown: "前\n\n![a.png](pending-image:abc)\n\n後" });
  await clock.advance(3_000);
  assert.deepEqual(server.writes()[0].args[0], { title: "", markdown: "前\n\n後" });
  assert.equal(session.getSnapshot().doc.markdown, "前\n\n![a.png](pending-image:abc)\n\n後");
});

test("a restored first payload never overwrites an article changed after creation (resend and lookup)", async () => {
  for (const path of ["resend", "lookup"] as const) {
    const clock = new FakeClock();
    const server = new FakeServer();
    const key = "restored-key";
    server.creations.set(key, "post-1");
    server.post = { id: "post-1", title: "initial", markdown: "first", version: 7, published: false, publishedAt: null, updatedAt: "", hasUnpublishedChanges: true };
    if (path === "lookup") server.responders.push(() => { throw new TypeError("response lost"); });
    else server.responders.push(() => ({ ...server.post! }));
    const created: string[] = [];
    const session = new ArticleSession(
      server,
      { creationKey: key, initialPayload: { title: "initial", markdown: "first" }, doc: { title: "initial", markdown: "restored local edits" } },
      { onCreated: (post) => created.push(post.id) },
      clock,
    );
    await clock.advance(3_000);
    assert.equal(session.getSnapshot().save, "conflict", path);
    assert.equal(session.getSnapshot().conflictServer?.version, 7);
    assert.deepEqual(created, ["post-1"], "the URL still learns the article ID");
    await clock.advance(120_000);
    assert.equal(server.calls.filter((c) => c.method === "save").length, 0, `${path}: no silent PUT`);
    assert.equal(server.post!.markdown, "first");
    assert.equal(session.getSnapshot().doc.markdown, "restored local edits");
    session.overwriteWithLocal();
    await clock.advance(0);
    assert.deepEqual(server.calls.at(-1)!.args, ["post-1", { title: "initial", markdown: "restored local edits" }, 7]);
  }
});

test("a first save rejected with 400 sends the corrected text after editing", async () => {
  const { clock, server, session } = setup();
  server.responders.push(() => {
    throw new ApiError(400, "本文が長すぎます");
  });
  session.setDocument({ title: "", markdown: "invalid" });
  await clock.advance(3_000);
  assert.equal(session.getSnapshot().save, "invalid");
  session.setDocument({ title: "", markdown: "corrected" });
  await clock.advance(3_000);
  const creates = server.calls.filter((c) => c.method === "create");
  assert.deepEqual(creates.map((c) => (c.args[0] as Doc).markdown), ["invalid", "corrected"]);
  assert.equal(session.getSnapshot().save, "saved");
});

test("verification errors: 403 stops, 429 waits for Retry-After, outcome stays unknown", async () => {
  const base: AdminPost = { id: "post-1", title: "t", markdown: "a", version: 1, published: false, publishedAt: null, updatedAt: "", hasUnpublishedChanges: true };
  {
    const { clock, server, session } = setup({ post: base });
    server.post = { ...base };
    server.responders.push(() => { throw new TypeError("lost"); });
    server.responders.push(() => { throw new ApiError(429, "slow down", 40); });
    session.setDocument({ title: "t", markdown: "ab" });
    await clock.advance(3_000);
    const gets = () => server.calls.filter((c) => c.method === "get").length;
    assert.equal(gets(), 1);
    await clock.advance(39_000);
    assert.equal(gets(), 1, "Retry-After respected");
    await clock.advance(1_000);
    assert.equal(gets(), 2);
    assert.equal(server.writes().length, 1);
  }
  {
    const { clock, server, session } = setup({ post: base });
    server.post = { ...base };
    server.responders.push(() => { throw new TypeError("lost"); });
    server.responders.push(() => { throw new ApiError(403, "forbidden"); });
    session.setDocument({ title: "t", markdown: "ab" });
    await clock.advance(3_000);
    assert.equal(session.getSnapshot().save, "forbidden");
    await clock.advance(300_000);
    assert.equal(server.calls.filter((c) => c.method === "get").length, 1, "403 is not retried");
  }
});

test("a first-payload conflict stays a conflict in the device copy, so a later restore cannot overwrite", async () => {
  for (const path of ["resend", "lookup"] as const) {
    const clock = new FakeClock();
    const server = new FakeServer();
    const key = "restored-key";
    server.creations.set(key, "post-1");
    server.post = { id: "post-1", title: "initial", markdown: "edited elsewhere", version: 7, published: false, publishedAt: null, updatedAt: "", hasUnpublishedChanges: true };
    if (path === "lookup") server.responders.push(() => { throw new TypeError("response lost"); });
    else server.responders.push(() => ({ ...server.post! }));
    const session = new ArticleSession(
      server,
      { creationKey: key, initialPayload: { title: "initial", markdown: "first" }, doc: { title: "initial", markdown: "restored local edits" } },
      {},
      clock,
    );
    await clock.advance(3_000);
    assert.equal(session.getSnapshot().save, "conflict", path);
    // What the device keeps: the article ID, and as base what this key created.
    const saved = session.recoveryState();
    assert.equal(saved.articleId, "post-1");
    assert.deepEqual(saved.base, { version: 1, doc: { title: "initial", markdown: "first" } }, path);
    assert.equal(saved.doc.markdown, "restored local edits");
    // After reload the editor compares this base with the server (7): conflict, not a plain restore.
    assert.notEqual(saved.base!.version, server.post!.version);
    session.dispose();
    assert.equal(server.calls.filter((c) => c.method === "save").length, 0, `${path}: no silent PUT`);
  }
});

test("typing while the first payload is written to the device never sends before that write completes", async () => {
  const clock = new FakeClock();
  const server = new FakeServer();
  let finishWrite!: () => void;
  const written: string[] = [];
  const session = new ArticleSession(
    server,
    {},
    {
      beforeCreate: (_key, payload) =>
        new Promise<void>((resolve) => {
          finishWrite = () => {
            written.push(payload.markdown);
            resolve();
          };
        }),
    },
    clock,
  );
  session.setDocument({ title: "t", markdown: "first" });
  const flushed = session.flush();
  await clock.advance(0);
  assert.equal(session.getSnapshot().save, "saving");
  session.setDocument({ title: "t", markdown: "first and more" });
  void session.flush();
  session.setComposing(false);
  await clock.advance(10_000);
  assert.equal(server.writes().length, 0, "nothing is sent while the device write is pending");
  finishWrite();
  await clock.advance(0);
  assert.deepEqual(written, ["first"]);
  const writes = server.writes();
  assert.equal(writes.filter((c) => c.method === "create").length, 1);
  assert.deepEqual(writes[0].args[0], { title: "t", markdown: "first" });
  assert.deepEqual(writes.at(-1)!.args[1], { title: "t", markdown: "first and more" }, "the later edit follows as an update");
  assert.equal(await flushed, true);
});

test("flush and publish wait for IME composition to end and send the confirmed text", async () => {
  const { clock, server, session } = setup();
  session.setDocument({ title: "IME", markdown: "確定済み" });
  await clock.advance(3_000);
  assert.equal(server.writes().length, 1);
  session.setComposing(true);
  session.setDocument({ title: "IME", markdown: "確定済みにほ" });
  let flushed: boolean | null = null;
  void session.flush().then((saved) => (flushed = saved));
  const published = session.publish();
  await clock.advance(60_000);
  assert.equal(server.writes().length, 1, "unconfirmed text is not sent");
  assert.equal(server.calls.filter((c) => c.method === "publish").length, 0);
  assert.equal(flushed, null);
  session.setDocument({ title: "IME", markdown: "確定済み日本" });
  session.setComposing(false);
  await clock.advance(0);
  await published;
  assert.deepEqual(server.writes().at(-1)!.args[1], { title: "IME", markdown: "確定済み日本" });
  assert.equal(flushed, true);
  const calls = server.calls.map((c) => c.method);
  assert.ok(calls.lastIndexOf("save") < calls.indexOf("publish"), "publication follows the confirmed save");
});
