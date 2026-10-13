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

const proxy = moduleFromSource("lib/backend-proxy.ts");
const basic = (username, password) => `Basic ${Buffer.from(`${username}:${password}`, "utf8").toString("base64")}`;
const traefikConfig = {
  backendURL: "http://main-be:8000",
  auth: { mode: "traefik", username: "internal", password: "internal:password", publicURL: "https://admin.example.test" },
};
const basicConfig = { ...traefikConfig, auth: { ...traefikConfig.auth, mode: "basic" } };

test("only the exact pairing payload accepts the extension and never attaches operator credentials", async () => {
  const origin = `chrome-extension://${"a".repeat(32)}`;
  let outbound;
  const payload = JSON.stringify({ pairing_token: "one-use-token", cookies: [] });
  const response = await proxy.proxyAPIRequest(new Request("https://admin.example.test/api/tiktok/connect/payload", { method: "POST", headers: { origin, "Content-Type": "application/json", authorization: basic("browser", "discard") }, body: payload }), ["tiktok", "connect", "payload"], traefikConfig, async (_url, init) => {
    outbound = init;
    assert.equal(new Headers(init.headers).get("authorization"), null);
    assert.equal(await new Response(init.body).text(), payload);
    return new Response("{}", { status: 200 });
  });
  assert.ok(outbound);
  assert.equal(response.status, 200);
  assert.equal(response.headers.get("access-control-allow-origin"), origin);
  const denied = await proxy.proxyAPIRequest(new Request("https://admin.example.test/api/tiktok/connect/payload/extra", { method: "POST", headers: { origin }, body: "{}" }), ["tiktok", "connect", "payload", "extra"], traefikConfig, async () => { throw new Error("must not forward"); });
  assert.equal(denied.status, 403);
  const web = await proxy.proxyAPIRequest(new Request("https://admin.example.test/api/tiktok/connect/payload", { method: "POST", headers: { origin: "https://other.example" }, body: "{}" }), ["tiktok", "connect", "payload"], traefikConfig, async () => { throw new Error("must not forward"); });
  assert.equal(web.status, 403);
});

test("operator proxy replaces browser authorization and preserves encoded IDs, query, status and body", async () => {
  let outbound;
  const request = new Request("https://admin.example.test/api/twitch/channels/channel%2Fid?state=ready%2Fnext", {
    method: "PATCH",
    headers: {
      authorization: basic("outer", "not-internal"),
      "x-terex-authenticated-user": "operator",
      host: "admin.example.test",
      origin: "https://admin.example.test",
      "content-type": "application/json",
      "x-untrusted": "must-not-pass",
    },
    body: JSON.stringify({ paused: true }),
  });
  const response = await proxy.proxyAPIRequest(request, ["twitch", "channels", "channel/id"], traefikConfig, async (url, init) => {
    outbound = { url: url.toString(), init, body: await new Response(init.body).text() };
    return new Response(JSON.stringify({ accepted: true }), { status: 202, headers: { "Content-Type": "application/json" } });
  });
  const sent = new Headers(outbound.init.headers);
  assert.equal(outbound.url, "http://main-be:8000/api/twitch/channels/channel%2Fid?state=ready%2Fnext");
  assert.equal(sent.get("authorization"), basic("internal", "internal:password"));
  assert.equal(sent.get("x-untrusted"), null);
  assert.equal(sent.get("x-terex-authenticated-user"), null);
  assert.equal(sent.get("origin"), "https://admin.example.test");
  assert.equal(outbound.body, JSON.stringify({ paused: true }));
  assert.equal(response.status, 202);
  assert.deepEqual(await response.json(), { accepted: true });
});

test("local collector Bearer is forwarded unchanged on its narrow route and never elevated to Basic", async () => {
  let authorization = "";
  const request = new Request("http://localhost:3000/api/imports", {
    method: "POST",
    headers: { authorization: "Bearer unverified-service-token" },
    body: "{}",
  });
  const response = await proxy.proxyAPIRequest(request, ["imports"], basicConfig, async (_url, init) => {
    authorization = new Headers(init.headers).get("authorization");
    return new Response(null, { status: 401, headers: { "WWW-Authenticate": "Bearer" } });
  });
  assert.equal(response.status, 401);
  assert.equal(authorization, "Bearer unverified-service-token");
  assert.equal(response.headers.get("www-authenticate"), null);
});

test("Traefik operator marker causes any incoming Bearer to be dropped and replaced internally", async () => {
  let authorization = "";
  const request = new Request("https://admin.example.test/api/imports", {
    method: "POST",
    headers: { authorization: "Bearer browser-value", "x-terex-authenticated-user": "operator" },
    body: "{}",
  });
  const response = await proxy.proxyAPIRequest(request, ["imports"], traefikConfig, async (_url, init) => {
    authorization = new Headers(init.headers).get("authorization");
    assert.equal(new Headers(init.headers).get("accept-encoding"), "identity");
    return new Response("ok", {
      status: 200,
      headers: {
        "X-Terex-Authenticated-User": "must-not-reach-client",
        Authorization: "Basic must-not-reach-client",
        "Proxy-Authorization": "Basic must-not-reach-client",
        "Content-Encoding": "gzip",
        "Content-Length": "999",
      },
    });
  });
  assert.equal(response.status, 200);
  assert.equal(authorization, basic("internal", "internal:password"));
  assert.equal(response.headers.get("x-terex-authenticated-user"), null);
  assert.equal(response.headers.get("authorization"), null);
  assert.equal(response.headers.get("proxy-authorization"), null);
  assert.equal(response.headers.get("content-encoding"), null);
  assert.equal(response.headers.get("content-length"), null);
  assert.equal(await response.text(), "ok");
});

test("Bearer on an operator route is rejected before any internal request", async () => {
  let calls = 0;
  const request = new Request("http://localhost:3000/api/jobs", { headers: { authorization: "Bearer unverified-service-token" } });
  const response = await proxy.proxyAPIRequest(request, ["jobs"], basicConfig, async () => {
    calls += 1;
    return new Response(null, { status: 204 });
  });
  assert.equal(response.status, 401);
  assert.equal(calls, 0);
  assert.equal(response.headers.get("www-authenticate"), 'Basic realm="Terex Admin", charset="UTF-8"');
});

test("OAuth callback is the exact public GET and retains the backend redirect", async () => {
  let authorization = "unexpected";
  const callback = new Request("https://admin.example.test/api/accounts/oauth/callback?state=one-use&code=provider-code", { headers: { host: "admin.example.test" } });
  const response = await proxy.proxyAPIRequest(callback, ["accounts", "oauth", "callback"], traefikConfig, async (_url, init) => {
    authorization = new Headers(init.headers).get("authorization");
    assert.equal(init.redirect, "manual");
    return new Response(null, { status: 303, headers: { Location: "https://admin.example.test/admin/accounts?oauth_connected=1" } });
  });
  assert.equal(authorization, null);
  assert.equal(response.status, 303);
  assert.equal(response.headers.get("location"), "https://admin.example.test/admin/accounts?oauth_connected=1");

  const invalidMethod = new Request("https://admin.example.test/api/accounts/oauth/callback", { method: "POST", headers: { host: "admin.example.test" } });
  const blocked = await proxy.proxyAPIRequest(invalidMethod, ["accounts", "oauth", "callback"], basicConfig, async () => {
    throw new Error("must not call main-be");
  });
  assert.equal(blocked.status, 401);
});

test("multipart bodies keep their content type and internal 401 never creates a second Basic challenge", async () => {
  const form = new FormData();
  form.append("name", "account");
  form.append("auth_file", new Blob(["credential payload"], { type: "application/json" }), "auth.json");
  const request = new Request("http://localhost:3000/api/accounts/create", {
    method: "POST",
    headers: { authorization: basic("internal", "internal:password") },
    body: form,
  });
  const response = await proxy.proxyAPIRequest(request, ["accounts", "create"], basicConfig, async (_url, init) => {
    const headers = new Headers(init.headers);
    assert.match(headers.get("content-type"), /^multipart\/form-data; boundary=/);
    assert.match(await new Response(init.body).text(), /credential payload/);
    return new Response("internal auth failure", { status: 401, headers: { "WWW-Authenticate": "Basic realm=internal" } });
  });
  assert.equal(response.status, 502);
  assert.equal(response.headers.get("www-authenticate"), null);
});

test("backend targets reject path escapes and non-fixed runtime URLs", () => {
  assert.equal(proxy.backendTarget("http://main-be:8000", [".."], ""), null);
  assert.equal(proxy.backendTarget("http://main-be:8000/api", ["jobs"], ""), null);
  assert.equal(proxy.backendTarget("http://user:pass@main-be:8000", ["jobs"], ""), null);
  assert.equal(proxy.backendTarget("http://main-be:8000", ["jobs"], "invalid"), null);
});

test("local collector may read the documented import resource without operator elevation", async () => {
  let authorization = "";
  const request = new Request("http://localhost:3000/api/imports/import%2Fid", {
    headers: { authorization: "Bearer unverified-service-token" },
  });
  const response = await proxy.proxyAPIRequest(request, ["imports", "import/id"], basicConfig, async (_url, init) => {
    authorization = new Headers(init.headers).get("authorization");
    return new Response("ready", { status: 200 });
  });
  assert.equal(response.status, 200);
  assert.equal(authorization, "Bearer unverified-service-token");
});
