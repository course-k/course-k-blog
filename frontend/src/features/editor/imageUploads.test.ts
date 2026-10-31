import test from "node:test";
import assert from "node:assert/strict";
import { ImageUploadQueue, rejectImage, type UploadApi } from "./imageUploads.ts";
import { ApiError, type UploadStatus } from "./adminApi.ts";

type Deferred = { resolve: () => void; reject: (error: Error) => void };

function fakeApi() {
  const sends: string[] = [];
  const begins: string[] = [];
  const pendingSends = new Map<string, Deferred>();
  const sessions = new Map<string, { status: UploadStatus["status"]; result?: UploadStatus["result"] }>();
  const failNames = new Set<string>();
  let completeLost = false;
  const api: UploadApi = {
    digest: async () => "0".repeat(64),
    wait: async () => {},
    begin: async (request) => {
      begins.push(request.clientImageId);
      const id = `upload-${request.clientImageId}`;
      const session = sessions.get(id) ?? { status: "pending" as const };
      sessions.set(id, session);
      if (session.status === "completed") return { uploadId: id, status: "completed", result: session.result };
      return { uploadId: id, status: session.status, upload: { method: "PUT", url: `/content/${request.fileName}`, expiresAt: "" } };
    },
    send: (target) =>
      new Promise((resolve, reject) => {
        const name = target.url.slice("/content/".length);
        sends.push(name);
        pendingSends.set(name, { resolve, reject });
      }),
    complete: async (uploadId) => {
      const name = uploadId.slice("upload-".length);
      if (failNames.has(name)) throw new ApiError(400, "画像は1200万画素以下にしてください。", null, { status: "failed" });
      const session = sessions.get(uploadId)!;
      session.status = "completed";
      session.result = { id: `img-${name}`, url: `/api/images/img-${name}`, width: 1, height: 1 };
      if (completeLost) {
        completeLost = false;
        throw new TypeError("response lost");
      }
      return { uploadId, status: "completed", result: session.result };
    },
    status: async (uploadId) => {
      const session = sessions.get(uploadId)!;
      return { uploadId, status: session.status, result: session.result };
    },
  };
  return {
    api,
    sends,
    begins,
    pendingSends,
    failNames,
    loseNextCompletion: () => {
      completeLost = true;
    },
  };
}

const settle = async () => {
  for (let i = 0; i < 30; i++) await Promise.resolve();
};

test("client checks name the format and 10,000,000-byte limit", () => {
  assert.equal(rejectImage({ type: "image/png", size: 10_000_000, name: "a.png" }), null);
  assert.match(rejectImage({ type: "image/png", size: 10_000_001, name: "a.png" })!, /10MB/);
  assert.match(rejectImage({ type: "image/png", size: 0, name: "a.png" })!, /0バイト/);
  for (const type of ["image/gif", "image/svg+xml", "image/heic", ""])
    assert.match(rejectImage({ type, size: 10, name: "a" })!, /PNG・JPEG・WebP/);
});

test("two uploads run at once, results arrive out of order and one failure stays isolated", async () => {
  const fake = fakeApi();
  fake.failNames.add("b");
  const done: string[] = [];
  const queue = new ImageUploadQueue(fake.api, { onChange() {}, onDone: (item) => done.push(item.clientImageId) });
  queue.add(["a", "b", "c"].map((name) => ({ clientImageId: `slot-${name}`, requestId: name, name, type: "image/png", blob: new Blob(["x"]) })));
  await settle();
  assert.deepEqual(fake.sends, ["a", "b"], "third waits for a free slot");
  fake.pendingSends.get("b")!.resolve();
  await settle();
  assert.deepEqual(fake.sends, ["a", "b", "c"]);
  fake.pendingSends.get("c")!.resolve();
  await settle();
  fake.pendingSends.get("a")!.resolve();
  await settle();
  assert.deepEqual(done, ["slot-c", "slot-a"], "completion order may differ from text order");
  assert.equal(queue.get("slot-b")!.status, "failed");
  assert.match(queue.get("slot-b")!.error!, /1200万画素/);
  assert.deepEqual(queue.list().map((item) => item.clientImageId), ["slot-a", "slot-b", "slot-c"]);
});

test("a lost completion response is resolved by reading status, without re-registering", async () => {
  const fake = fakeApi();
  fake.loseNextCompletion();
  const done: string[] = [];
  const queue = new ImageUploadQueue(fake.api, { onChange() {}, onDone: (_item, result) => done.push(result.url) });
  queue.add([{ clientImageId: "slot", requestId: "a", name: "a", type: "image/png", blob: new Blob(["x"]) }]);
  await settle();
  fake.pendingSends.get("a")!.resolve();
  await settle();
  assert.deepEqual(done, ["/api/images/img-a"]);
  // Retrying a finished slot does nothing; a new begin for the same request returns the result.
  queue.retry("slot");
  await settle();
  assert.equal(fake.sends.length, 1);
});

test("retry reuses the request ID; expiry gets a new one but keeps the slot", async () => {
  const fake = fakeApi();
  const queue = new ImageUploadQueue(fake.api, { onChange() {}, onDone() {} });
  queue.add([{ clientImageId: "slot", requestId: "r1", name: "a", type: "image/png", blob: new Blob(["x"]) }]);
  await settle();
  fake.pendingSends.get("a")!.reject(new Error("network"));
  await settle();
  fake.pendingSends.get("a")!.reject(new Error("network"));
  await settle();
  fake.pendingSends.get("a")!.reject(new Error("network"));
  await settle();
  assert.equal(queue.get("slot")!.status, "failed");
  queue.retry("slot");
  await settle();
  assert.deepEqual(fake.begins, ["r1", "r1", "r1", "r1"]);
  const item = queue.get("slot")!;
  item.status = "failed";
  item.expired = true;
  queue.retry("slot");
  assert.notEqual(queue.get("slot")!.requestId, "r1");
  assert.equal(queue.get("slot")!.clientImageId, "slot");
});

test("cancelling a deleted slot stops it and frees the slot for the next upload", async () => {
  const fake = fakeApi();
  const done: string[] = [];
  const queue = new ImageUploadQueue(fake.api, { onChange() {}, onDone: (item) => done.push(item.clientImageId) }, 1);
  queue.add([
    { clientImageId: "slot-a", requestId: "a", name: "a", type: "image/png", blob: new Blob(["x"]) },
    { clientImageId: "slot-b", requestId: "b", name: "b", type: "image/png", blob: new Blob(["x"]) },
  ]);
  await settle();
  queue.cancel("slot-a");
  await settle();
  assert.deepEqual(fake.sends, ["a", "b"]);
  fake.pendingSends.get("a")!.resolve();
  fake.pendingSends.get("b")!.resolve();
  await settle();
  assert.deepEqual(done, ["slot-b"], "a deleted slot never comes back");
});

test("dispose stops for good: queued uploads never start afterwards", async () => {
  const fake = fakeApi();
  const done: string[] = [];
  const queue = new ImageUploadQueue(fake.api, { onChange() {}, onDone: (item) => done.push(item.clientImageId) });
  queue.add(["a", "b", "c"].map((name) => ({ clientImageId: `slot-${name}`, requestId: name, name, type: "image/png", blob: new Blob(["x"]) })));
  await settle();
  assert.deepEqual(fake.sends, ["a", "b"]);
  queue.dispose();
  fake.pendingSends.get("a")!.resolve();
  fake.pendingSends.get("b")!.resolve();
  await settle();
  assert.deepEqual(fake.sends, ["a", "b"], "the queued third upload is not started");
  assert.deepEqual(done, []);
  queue.add([{ clientImageId: "late", requestId: "d", name: "d", type: "image/png", blob: new Blob(["x"]) }]);
  await settle();
  assert.deepEqual(fake.sends, ["a", "b"]);
});

test("a completion stopped mid-way resumes once the server reports it as pending", async () => {
  const statuses: string[] = ["processing", "processing", "pending"];
  let completes = 0;
  const api: UploadApi = {
    digest: async () => "0".repeat(64),
    wait: async () => {},
    begin: async () => ({ uploadId: "u", status: "processing" }),
    send: async () => {},
    status: async () => ({ uploadId: "u", status: (statuses.shift() ?? "pending") as UploadStatus["status"] }),
    complete: async () => {
      completes++;
      return { uploadId: "u", status: "completed", result: { id: "i", url: "/api/images/i", width: 1, height: 1 } };
    },
  };
  const done: string[] = [];
  const queue = new ImageUploadQueue(api, { onChange() {}, onDone: (_item, result) => done.push(result.url) });
  queue.add([{ clientImageId: "slot", requestId: "r", name: "a", type: "image/png", blob: new Blob(["x"]) }]);
  await settle();
  await settle();
  assert.equal(completes, 1);
  assert.deepEqual(done, ["/api/images/i"]);
});

test("401 marks the image as waiting for login and resumes it after login", async () => {
  let authFailures = 1;
  let expired = 0;
  const fake = fakeApi();
  const begin = fake.api.begin;
  fake.api.begin = async (request) => {
    if (authFailures-- > 0) throw new ApiError(401, "login required");
    return begin(request);
  };
  const queue = new ImageUploadQueue(fake.api, { onChange() {}, onDone() {}, onAuthExpired: () => expired++ });
  queue.add([{ clientImageId: "slot", requestId: "r", name: "a", type: "image/png", blob: new Blob(["x"]) }]);
  await settle();
  assert.equal(expired, 1);
  assert.equal(queue.get("slot")!.status, "failed");
  assert.equal(queue.get("slot")!.authExpired, true);
  queue.resumeAfterLogin();
  await settle();
  assert.deepEqual(fake.sends, ["a"]);
});
