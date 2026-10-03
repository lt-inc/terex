const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const assert = require("node:assert/strict");
const ts = require("typescript");
function moduleFromSource(file, fetchMock) {
  const filename = path.resolve(__dirname, "..", "src", file);
  const source = ts.transpileModule(fs.readFileSync(filename, "utf8"), {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
    },
  }).outputText;
  const exports = {};
  new Function("exports", "require", "fetch", source)(
    exports,
    (p) =>
      moduleFromSource(
        path.relative(
          path.resolve(__dirname, "..", "src"),
          path.resolve(path.dirname(filename), p + ".ts"),
        ),
        fetchMock,
      ),
    fetchMock,
  );
  return exports;
}
const dates = moduleFromSource("lib/datetime.ts");
const validation = moduleFromSource("lib/validation.ts");
const response = (status, data) => ({
  ok: status >= 200 && status < 300,
  status,
  text: async () => JSON.stringify(data),
});
test("Tomsk input has one timezone conversion and roundtrips", () => {
  assert.equal(
    dates.zonePartsToISO("2026-10-03", "12:00", "Asia/Tomsk"),
    "2026-10-03T05:00:00.000Z",
  );
  assert.deepEqual(dates.zonedParts("2026-10-03T05:00:00Z", "Asia/Tomsk"), {
    date: "2026-10-03",
    time: "12:00",
  });
});
test("timezone display changes preserve the instant and nonexistent DST times fail", () => {
  const instant = "2026-10-03T05:00:00Z";
  assert.deepEqual(dates.zonedParts(instant, "Europe/Moscow"), {
    date: "2026-10-03",
    time: "08:00",
  });
  assert.throws(
    () => dates.zonePartsToISO("2026-03-29", "02:30", "Europe/Berlin"),
    /does not exist/,
  );
});
test("calendar starts Monday and month navigation crosses long months", () => {
  assert.equal(dates.calendarDays("2026-10-03", "week")[0], "2026-09-28");
  assert.equal(dates.calendarDays("2026-10-03", "month").length, 42);
  assert.equal(dates.shiftMonth("2026-10-03", 1), "2026-11-01");
});
test("targets require independent starts and valid intervals while channel templates can omit start", () => {
  const targets = [
    {
      account_id: 1,
      start_at: "2026-10-03T05:00:00Z",
      interval_seconds: 86400,
      timezone: "Asia/Tomsk",
    },
    {
      account_id: 2,
      start_at: "2026-10-03T08:00:00Z",
      interval_seconds: 3600,
      timezone: "Asia/Tomsk",
    },
  ];
  assert.equal(validation.validateTargets(targets), null);
  assert.match(
    validation.validateTargets([{ ...targets[0], interval_seconds: NaN }]),
    /interval/,
  );
  assert.match(
    validation.validateTargets([{ ...targets[0], start_at: null }]),
    /start time/,
  );
  assert.equal(
    validation.validateTargets([{ ...targets[0], start_at: null }], false),
    null,
  );
});
test("adding an empty banner never reuses hidden base targets", () => {
  assert.match(
    validation.validateVideoCard({
      youtubeUrl: "https://youtube.com/watch?v=abc",
      sliceMode: "highlights",
      targets: [
        {
          account_id: 1,
          start_at: "2026-10-03T05:00:00Z",
          interval_seconds: 3600,
        },
      ],
      banners: [{ bannerName: "banner", targets: [] }],
    }),
    /Select at least/,
  );
});
test("editor rejects invalid bounds and subtitle timestamps", () => {
  const draft = {
    start_sec: 5,
    end_sec: 15,
    source_duration_sec: 30,
    title: "Title",
    description: "",
    cues: [{ id: "a", start: 6, end: 8, text: "Hello" }],
  };
  assert.equal(validation.validateEditor(draft), null);
  assert.match(validation.validateEditor({ ...draft, end_sec: 31 }), /source/);
  assert.match(
    validation.validateEditor({
      ...draft,
      cues: [{ id: "a", start: 8, end: 6, text: "Hi" }],
    }),
    /timestamps/,
  );
});
test("HTTP errors surface the backend detail and failed deletion does not succeed", async () => {
  const api = moduleFromSource("lib/api.ts", async () =>
    response(409, { detail: "state changed" }),
  );
  await assert.rejects(
    api.deleteAccount(1),
    (e) => e.status === 409 && e.message === "state changed",
  );
});
test("batch submission preserves partial failures and sends RFC3339 account schedules unchanged", async () => {
  const seen = [];
  const api = moduleFromSource("lib/api.ts", async (url, init) => {
    const body = JSON.parse(init.body);
    seen.push(body);
    return body.id === "ok"
      ? response(200, { status: "queued", job_id: "job" })
      : response(500, { detail: "queue unavailable" });
  });
  const target = {
    account_id: 3,
    start_at: "2026-10-03T05:00:00Z",
    interval_seconds: 86400,
    timezone: "Asia/Tomsk",
  };
  const results = await api.submitJobs([
    { id: "ok", targets: [target] },
    { id: "fail", targets: [target] },
  ]);
  assert.equal(results[0].ok, true);
  assert.equal(results[1].ok, false);
  assert.equal(results[1].error, "queue unavailable");
  assert.deepEqual(seen[0].targets[0], target);
});
test("publication and editorial mutations carry expected_version", async () => {
  const seen = [];
  const api = moduleFromSource("lib/api.ts", async (url, init) => {
    seen.push({ url, body: JSON.parse(init.body) });
    return response(200, {});
  });
  await api.actPublication(7, "cancel", 4);
  await api.updatePublication(7, {
    version: 4,
    publish_at: "2026-10-03T05:00:00Z",
    timezone: "Asia/Tomsk",
  });
  await api.saveClipEditor("clip", { version: 8, cues: [] });
  assert.equal(seen[0].body.expected_version, 4);
  assert.equal(seen[1].body.expected_version, 4);
  assert.equal(seen[2].body.expected_version, 8);
});
test("target updates and OAuth confirmation carry exact IDs and version", async () => {
  const seen = [];
  const api = moduleFromSource("lib/api.ts", async (url, init) => {
    seen.push({ url, body: JSON.parse(init.body) });
    return response(200, {});
  });
  const targets = [
    {
      account_id: 11,
      start_at: "2026-10-03T05:00:00Z",
      interval_seconds: 3600,
      timezone: "Asia/Tomsk",
    },
  ];
  await api.saveSubJobTargets("sub", 6, targets);
  await api.confirmAccount({
    id: 11,
    version: 2,
    remote_account_id: "UCactual",
  });
  await api.restoreClipRevision("clip", 9, 123);
  await api.connectAccount({ id: 11, version: 2 });
  await api.disconnectAccount({ id: 11, version: 3 });
  assert.deepEqual(seen[0].body, { expected_version: 6, targets });
  assert.deepEqual(seen[1].body, {
    expected_version: 2,
    remote_account_id: "UCactual",
  });
  assert.deepEqual(seen[2].body, { expected_version: 9, revision_id: 123 });
  assert.deepEqual(seen[3].body, { expected_version: 2 });
  assert.deepEqual(seen[4].body, { expected_version: 3 });
});
