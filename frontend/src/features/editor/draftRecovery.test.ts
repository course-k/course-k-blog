import test from "node:test";
import assert from "node:assert/strict";
import {
  MemoryRecoveryStore,
  RECOVERY_RETENTION_MS,
  RECOVERY_SCHEMA,
  RecoveryWriter,
  recoverableRecords,
  tabSessionId,
  type RecoveryRecord,
} from "./draftRecovery.ts";

const record = (key: string, tab: string, updatedAt: number): RecoveryRecord => ({
  schema: RECOVERY_SCHEMA,
  key,
  tabSessionId: tab,
  localId: key,
  articleId: null,
  creationKey: "k",
  initialPayload: null,
  base: null,
  doc: { title: key, markdown: "" },
  pendingImages: [{ clientImageId: "c", requestId: "r", name: "a.png", type: "image/png", blob: new Blob(["png"]) }],
  updatedAt,
});

test("expired records are removed; live tabs and this tab are not offered", async () => {
  const store = new MemoryRecoveryStore();
  const now = 100 * RECOVERY_RETENTION_MS;
  await store.put(record("old", "dead-tab", now - RECOVERY_RETENTION_MS - 1));
  await store.put(record("mine", tabSessionId, now));
  await store.put(record("other-live", "live-tab", now));
  await store.put(record("newer", "dead-tab", now - 10));
  await store.put(record("older", "dead-tab", now - 20));
  const offered = await recoverableRecords(store, now, new Set(["live-tab"]));
  assert.deepEqual(offered.map((r) => r.key), ["newer", "older"]);
  assert.equal(store.records.has("old"), false);
  assert.equal(store.records.has("other-live"), true, "another tab's record is never deleted");
  assert.equal(offered[0].pendingImages[0].blob instanceof Blob, true);
});

test("writer debounces to 250 ms, caps at 1 s while typing and reports unavailable storage", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout", "Date"] });
  const store = new MemoryRecoveryStore();
  let unavailable = 0;
  const writer = new RecoveryWriter(store, () => unavailable++);
  let n = 0;
  const build = () => record("draft", tabSessionId, ++n);
  for (let i = 0; i < 9; i++) {
    writer.schedule(build, Date.now());
    t.mock.timers.tick(120);
    await Promise.resolve();
  }
  // Typing every 120 ms: the first write still happens within 1 s.
  assert.equal(store.records.size, 1);
  store.failWrites = true;
  writer.schedule(build, Date.now());
  t.mock.timers.tick(250);
  for (let i = 0; i < 5; i++) await Promise.resolve();
  assert.equal(unavailable, 1);
});
