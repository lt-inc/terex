const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const assert = require("node:assert/strict");
const ts = require("typescript");

function moduleFromSource(file, fetchMock) {
  const filename = path.resolve(__dirname, "..", "src", file);
  const source = ts.transpileModule(fs.readFileSync(filename, "utf8"), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  const exports = {};
  new Function("exports", "require", "fetch", source)(exports, (name) => moduleFromSource(
    path.relative(path.resolve(__dirname, "..", "src"), path.resolve(path.dirname(filename), `${name}.ts`)), fetchMock,
  ), fetchMock);
  return exports;
}
const access = moduleFromSource("lib/admin-access.ts");
const twitch = moduleFromSource("lib/twitch.ts");
const source = moduleFromSource("lib/job-source.ts");
const basic = (username, password) => `Basic ${Buffer.from(`${username}:${password}`, "utf8").toString("base64")}`;
const config = { production: true, username: "admin", password: "secret:colon" };

test("production and partially configured administrator access fail closed", () => {
  assert.equal(access.adminAccess("/admin/twitch", "GET", null, { production: true }), "unconfigured");
  assert.equal(access.adminAccess("/api/jobs", "POST", null, { production: false, username: "admin" }), "unconfigured");
  assert.equal(access.adminAccess("/admin/twitch", "GET", null, { production: false }), "allow");
  assert.equal(access.adminAccess("/", "GET", null, config), "authenticate");
  assert.equal(access.adminAccess("/api/compilations", "POST", basic("admin", "wrong"), config), "authenticate");
  assert.equal(access.adminAccess("/api/compilations", "POST", basic("admin", "secret:colon"), config), "allow");
});
test("Basic parsing supports UTF-8 and colon passwords, rejecting malformed credentials", () => {
  assert.deepEqual(access.basicCredentials(basic("оператор", "пароль:ещё")), ["оператор", "пароль:ещё"]);
  assert.equal(access.basicCredentials("Basic $$$"), null);
  assert.equal(access.basicCredentials("Basic /w=="), null);
  assert.equal(access.basicCredentials("Basic YWRtaW4="), null);
  assert.equal(access.basicCredentials(`Basic ${"A".repeat(5000)}`), null);
});
test("service Bearer only passes through ingestion and settings reads for backend verification", () => {
  const accepted = [["/api/twitch/settings", "GET"], ["/api/imports", "GET"], ["/api/imports", "POST"], ["/api/imports/source-id/complete", "POST"]];
  for (const [route, method] of accepted) assert.equal(access.adminAccess(route, method, "Bearer service", config), "allow");
  const denied = [["/admin/twitch", "GET"], ["/api/twitch/settings", "PUT"], ["/api/imports/id", "PATCH"], ["/api/clips/id/approve", "POST"], ["/api/publications/id/publish-now", "POST"], ["/api/compilations", "POST"], ["/api/imports/a/b/complete", "POST"]];
  for (const [route, method] of denied) assert.equal(access.adminAccess(route, method, "Bearer service", config), "authenticate");
  assert.equal(access.adminAccess("/api/imports", "POST", "Bearer ", config), "authenticate");
});
test("only the exact GET OAuth callback bypasses administrator authentication", () => {
  assert.equal(access.adminAccess("/api/accounts/oauth/callback", "GET", null, config), "allow");
  assert.equal(access.adminAccess("/api/accounts/oauth/callback", "POST", null, config), "authenticate");
  assert.equal(access.adminAccess("/api/accounts/oauth/callback/extra", "GET", null, config), "authenticate");
});
test("Twitch defaults stay disabled and contradictory budgets or windows are rejected", () => {
  const defaults = twitch.DEFAULT_TWITCH_SETTINGS;
  assert.equal(defaults.enabled, false);
  assert.equal(defaults.target_account_id, 0);
  assert.equal(twitch.validateTwitchSettings(defaults), null);
  assert.match(twitch.validateTwitchSettings({ ...defaults, enabled: true }), /account/);
  assert.match(twitch.validateTwitchSettings({ ...defaults, per_channel_daily_downloads: 11 }), /per-channel/);
  assert.match(twitch.validateTwitchSettings({ ...defaults, min_age_minutes: 72 * 60 }), /discovery window/);
  assert.match(twitch.validateTwitchSettings({ ...defaults, min_duration_sec: NaN }), /durations/);
  assert.match(twitch.validateTwitchSettings({ ...defaults, timezone: "invalid" }), /timezone/);
  assert.match(twitch.validateTwitchSettings({ ...defaults, first: 101 }), /whole number/);
});
test("compilation candidates exclude rejected, failed and unfinished imports", () => {
  const ready = { import_id: "ready", state: "ready", clip_id: "clip", revision_id: 3, rejected: false };
  assert.deepEqual(twitch.eligibleImports([ready, { ...ready, import_id: "rejected", rejected: true }, { ...ready, state: "pending" }, { ...ready, revision_id: 0 }, { ...ready, error: "render failed" }]), [ready]);
});
test("reordering preserves pinned revisions; invalid bounds and duplicated clips cannot be saved", () => {
  const items = [1, 2, 3].map((id) => ({ clip_id: `clip-${id}`, revision_id: id * 10, start_sec: 0, end_sec: 15 }));
  assert.equal(twitch.validateCompilation("Title", items), null);
  const moved = twitch.moveCompilationItem(items, 0, 1);
  assert.deepEqual(moved.map((item) => item.revision_id), [20, 10, 30]);
  assert.deepEqual(items.map((item) => item.revision_id), [10, 20, 30]);
  assert.match(twitch.validateCompilation("Title", [items[0], items[0], items[2]]), /only appear once/);
  assert.match(twitch.validateCompilation("Title", [{ ...items[0], end_sec: 0 }, items[1], items[2]]), /bounds/);
  assert.match(twitch.validateCompilation("Title", [items[0]]), /3–10/);
});
test("legacy YouTube jobs and imported sources retain their labels and URLs", () => {
  assert.equal(source.jobSourceLabel({ youtube_url: "https://youtube.com/watch?v=x" }), "YouTube");
  assert.equal(source.jobSourceURL({ youtube_url: "legacy" }), "legacy");
  assert.equal(source.jobSourceURL({ source_url: "twitch", youtube_url: "legacy" }), "twitch");
  assert.equal(source.jobSourceLabel({ source_kind: "twitch_clip" }), "Twitch clip");
  assert.equal(source.jobSourceLabel({ source_kind: "compilation" }), "Compilation");
  assert.equal(source.montageLabel("full_clip"), "Whole clip");
});
test("API writes preserve string channel IDs, complete settings, ordered revisions and optimistic version", async () => {
  const calls = [];
  const api = moduleFromSource("lib/api.ts", async (url, init) => {
    calls.push({ url, method: init?.method ?? "GET", body: init?.body ? JSON.parse(init.body) : null });
    return { ok: true, status: 200, text: async () => JSON.stringify({}) };
  });
  const spec = { title: "Clips", items: [{ clip_id: "a", revision_id: 33, start_sec: 2, end_sec: 10 }, { clip_id: "b", revision_id: 44, start_sec: 0, end_sec: 9 }] };
  await api.updateTwitchChannel("channel/id", true);
  await api.saveTwitchSettings(twitch.DEFAULT_TWITCH_SETTINGS);
  await api.saveCompilation("comp/id", 7, spec);
  await api.rejectImport("source/id");
  await api.fetchImports("ready");
  assert.deepEqual(calls[0], { url: "/api/twitch/channels/channel%2Fid", method: "PATCH", body: { paused: true } });
  assert.deepEqual(calls[1].body, twitch.DEFAULT_TWITCH_SETTINGS);
  assert.deepEqual(calls[2].body, { expected_version: 7, ...spec });
  assert.deepEqual(calls[3].body, { rejected: true });
  assert.equal(calls[4].url, "/api/imports?state=ready");
});
test("source-state and compilation version conflicts remain actionable API errors", async () => {
  const api = moduleFromSource("lib/api.ts", async () => ({ ok: false, status: 409, text: async () => JSON.stringify({ detail: "source rejected or revision changed" }) }));
  await assert.rejects(api.saveCompilation("id", 2, { title: "Title", items: [] }), (error) => error.status === 409 && /source rejected/.test(error.message));
});
