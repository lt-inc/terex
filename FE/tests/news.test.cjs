const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const assert = require("node:assert/strict");
const ts = require("typescript");

function load(file) {
  const filename = path.resolve(__dirname, "..", "src", file);
  const source = ts.transpileModule(fs.readFileSync(filename, "utf8"), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  const exports = {};
  new Function("exports", "require", source)(exports, () => ({}));
  return exports;
}
const news = load("lib/news-flow.ts");
const valid = () => ({ ...news.DEFAULT_NEWS_FLOW, name: "Crypto" });

test("a default flow with a name is valid and starts disabled in review mode", () => {
  const flow = valid();
  assert.equal(news.validateNewsFlow(flow), null);
  assert.equal(flow.enabled, false);
  assert.equal(flow.approve_mode, "by_approve");
});

test("validation mirrors the collector's limits", () => {
  const bad = (patch) => news.validateNewsFlow({ ...valid(), ...patch });
  assert.match(bad({ name: " " }), /название/i);
  assert.ok(bad({ posts_per_day: 0 }));
  assert.ok(bad({ posts_per_day: 49 }));
  assert.ok(bad({ window_start_hour: 22, window_end_hour: 9 }));
  assert.ok(bad({ window_end_hour: 25 }));
  assert.ok(bad({ min_relevance: 1.5 }));
  assert.ok(bad({ max_chars: 100 }));
  assert.ok(bad({ model: "two words" }));
  assert.ok(bad({ posts_per_day: 1.5 }));
});

test("a flow cannot be enabled without a channel and sources", () => {
  assert.match(news.validateNewsFlow({ ...valid(), enabled: true }), /канал/i);
  assert.match(news.validateNewsFlow({ ...valid(), enabled: true, channel_id: 1 }), /источник/i);
  assert.equal(news.validateNewsFlow({ ...valid(), enabled: true, channel_id: 1, source_ids: [3] }), null);
});

test("blockers explain why a flow is silent", () => {
  const flow = { ...valid(), id: 1 };
  assert.equal(news.flowBlockers(flow, true).length, 3);
  assert.ok(news.flowBlockers({ ...flow, enabled: true, channel_id: 1, source_ids: [1] }, false)[0].includes("OPENROUTER_API_KEY"));
  assert.deepEqual(news.flowBlockers({ ...flow, enabled: true, channel_id: 1, source_ids: [1] }, true), []);
});

test("relative time is readable and tolerant", () => {
  const now = Date.parse("2026-10-12T12:00:00Z");
  assert.equal(news.relativeTime(null, now), "—");
  assert.equal(news.relativeTime("garbage", now), "—");
  assert.equal(news.relativeTime("2026-10-12T11:59:40Z", now), "только что");
  assert.equal(news.relativeTime("2026-10-12T11:30:00Z", now), "30 мин назад");
  assert.equal(news.relativeTime("2026-10-12T09:00:00Z", now), "3 ч назад");
  assert.equal(news.relativeTime("2026-10-10T12:00:00Z", now), "2 дн назад");
});

test("every post status has a label and a style", () => {
  for (const status of ["pending", "approved", "published", "failed", "rejected", "expired"]) {
    assert.ok(news.POST_STATUS_LABEL[status] && news.POST_STATUS_STYLE[status], status);
  }
});
