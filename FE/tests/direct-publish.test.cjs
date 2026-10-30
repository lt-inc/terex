const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const assert = require("node:assert/strict");
const ts = require("typescript");

function load(file) {
  const filename = path.resolve(__dirname, "..", "src", file);
  const source = ts.transpileModule(fs.readFileSync(filename, "utf8"), {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
    },
  }).outputText;
  const exports = {};
  new Function("exports", source)(exports);
  return exports;
}
const lib = load("lib/direct-publish.ts");

function memory() {
  const map = new Map();
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
    removeItem: (k) => map.delete(k),
  };
}
const request = {
  request_id: "3f2b8c1e-5a4d-4e8f-9b7a-1c2d3e4f5a6b",
  revision_id: 7,
  expected_version: 3,
  media_kind: "source",
  account_id: 11,
  platform: "tiktok",
  caption: "Подпись #tag",
  visibility: "private",
  reviewed: true,
};

test("an unconfirmed request survives reload with the exact payload and UUID", () => {
  const storage = memory();
  lib.savePendingRequest(storage, "clip-1", request);
  assert.deepEqual(lib.loadPendingRequest(storage, "clip-1"), request);
  assert.equal(lib.loadPendingRequest(storage, "clip-2"), null);
  lib.clearPendingRequest(storage, "clip-1");
  assert.equal(lib.loadPendingRequest(storage, "clip-1"), null);
});

test("corrupt or tampered stored requests are ignored instead of replayed", () => {
  const storage = memory();
  storage.setItem("terex.direct-publish.v1.c", "{not json");
  assert.equal(lib.loadPendingRequest(storage, "c"), null);
  storage.setItem(
    "terex.direct-publish.v1.c",
    JSON.stringify({ ...request, request_id: "nope" }),
  );
  assert.equal(lib.loadPendingRequest(storage, "c"), null);
  storage.setItem(
    "terex.direct-publish.v1.c",
    JSON.stringify({ ...request, reviewed: false }),
  );
  assert.equal(lib.loadPendingRequest(storage, "c"), null);
});

test("only a definite 4xx answer releases the request; ambiguity keeps it", () => {
  assert.equal(lib.isDefiniteRejection({ status: 400 }), true);
  assert.equal(lib.isDefiniteRejection({ status: 409 }), true);
  assert.equal(lib.isDefiniteRejection({ status: 422 }), true);
  for (const status of [408, 425, 429, 500, 502, 503, 504])
    assert.equal(lib.isDefiniteRejection({ status }), false);
  assert.equal(lib.isDefiniteRejection(new TypeError("Failed to fetch")), false);
  assert.equal(lib.isDefiniteRejection(new Error("aborted")), false);
});

test("request UUIDs are valid v4 and unique", () => {
  const a = lib.newRequestID();
  assert.match(a, /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  assert.notEqual(a, lib.newRequestID());
});

test("TikTok needs a connected account with a session; TikTok has no unlisted", () => {
  const base = { id: 1, name: "a", connection_state: "connected" };
  assert.equal(lib.accountReadiness({ ...base, platform: "youtube" }).ready, true);
  assert.equal(lib.accountReadiness({ ...base, platform: "tiktok" }).ready, false);
  assert.equal(
    lib.accountReadiness({ ...base, platform: "tiktok", session_version: 2 }).ready,
    true,
  );
  assert.equal(
    lib.accountReadiness({
      ...base,
      platform: "tiktok",
      session_version: 2,
      connection_state: "disconnected",
    }).ready,
    false,
  );
  assert.equal(lib.accountReadiness({ ...base, platform: "instagram" }).ready, false);
  assert.deepEqual(
    lib.visibilityOptions("tiktok").map((o) => o.value),
    ["private", "public"],
  );
  assert.deepEqual(
    lib.visibilityOptions("youtube").map((o) => o.value),
    ["private", "unlisted", "public"],
  );
});
