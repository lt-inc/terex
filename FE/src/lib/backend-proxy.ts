import {
  accessDetail,
  accessStatus,
  adminAccess,
  requiresBasicChallenge,
  type AdminAccess,
  type AdminAccessConfig,
  TRAEFIK_AUTHENTICATED_USER_HEADER,
} from "./admin-access";

export interface BackendProxyConfig {
  backendURL?: string;
  auth: AdminAccessConfig;
}

type Fetcher = (input: URL, init: RequestInit) => Promise<Response>;

const REQUEST_HEADERS = ["accept", "accept-language", "content-type", "if-match", "if-none-match", "origin", "range"];
const HOP_BY_HOP_RESPONSE_HEADERS = new Set([
  "authorization",
  "connection",
  "content-encoding",
  "content-length",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
  "www-authenticate",
  TRAEFIK_AUTHENTICATED_USER_HEADER,
]);

function jsonResponse(status: number, detail: string, challenge = false): Response {
  const headers = new Headers({ "Content-Type": "application/json", "Cache-Control": "no-store" });
  if (challenge) headers.set("WWW-Authenticate", 'Basic realm="Terex Admin", charset="UTF-8"');
  return new Response(JSON.stringify({ detail }), { status, headers });
}

function accessFailure(access: AdminAccess, config: AdminAccessConfig): Response {
  return jsonResponse(accessStatus(access, config), accessDetail(access), requiresBasicChallenge(access, config));
}

// The destination comes only from runtime configuration. The caller supplies
// path segments, which are encoded one-by-one so a decoded ID cannot escape /api.
export function backendTarget(backendURL: string | undefined, segments: readonly string[], search: string): URL | null {
  if (!backendURL || backendURL.length > 2048 || (search !== "" && !search.startsWith("?"))) return null;
  try {
    const base = new URL(backendURL);
    if (
      (base.protocol !== "http:" && base.protocol !== "https:")
      || !base.host
      || base.username
      || base.password
      || base.pathname !== "/"
      || base.search
      || base.hash
      || segments.length === 0
    ) return null;
    const encoded = segments.map((segment) => {
      if (!segment || segment === "." || segment === ".." || /[\u0000\r\n]/.test(segment)) throw new Error("unsafe path segment");
      return encodeURIComponent(segment);
    });
    const target = new URL(base.origin);
    target.pathname = `/api/${encoded.join("/")}`;
    target.search = search;
    return target;
  } catch {
    return null;
  }
}

function forwardedHeaders(input: Headers, access: AdminAccess, config: AdminAccessConfig): Headers {
  const headers = new Headers();
  for (const name of REQUEST_HEADERS) {
    const value = input.get(name);
    if (value) headers.set(name, value);
  }
  // Node fetch transparently decompresses some upstream responses. Ask for an
  // identity response and remove encoding/length on the way back as a second
  // guard, so a client never receives stale compression metadata.
  headers.set("Accept-Encoding", "identity");
  if (access === "operator") {
    const credentials = `${config.username!}:${config.password!}`;
    headers.set("Authorization", `Basic ${Buffer.from(credentials, "utf8").toString("base64")}`);
  } else if (access === "service") {
    // adminAccess permits this only for the narrow collector routes. Preserve
    // the unverified token for main-be to validate; never replace it with Basic.
    headers.set("Authorization", input.get("authorization")!);
  }
  return headers;
}

function responseFromBackend(response: Response, access: AdminAccess): Response {
  // A 401 after the proxy attached its own Basic credential is an internal
  // configuration failure. A collector Bearer response stays visible to the
  // caller, but its WWW-Authenticate challenge is still never relayed.
  if (response.status === 401 && access === "operator") return jsonResponse(502, "Internal API authentication failed.");
  const headers = new Headers();
  response.headers.forEach((value, name) => {
    if (!HOP_BY_HOP_RESPONSE_HEADERS.has(name.toLowerCase())) headers.append(name, value);
  });
  return new Response(response.body, { status: response.status, statusText: response.statusText, headers });
}

export async function proxyAPIRequest(
  request: Request,
  segments: readonly string[],
  config: BackendProxyConfig,
  fetcher: Fetcher = fetch,
): Promise<Response> {
  const inbound = new URL(request.url);
  const access = adminAccess(inbound.pathname, request.method, request.headers, config.auth);
  if (access !== "operator" && access !== "service" && access !== "oauth_callback") return accessFailure(access, config.auth);

  const target = backendTarget(config.backendURL, segments, inbound.search);
  if (!target) return jsonResponse(503, "Backend API is not configured.");

  const init: RequestInit & { duplex?: "half" } = {
    method: request.method,
    headers: forwardedHeaders(request.headers, access, config.auth),
    redirect: "manual",
    cache: "no-store",
  };
  if (request.method !== "GET" && request.method !== "HEAD" && request.body) {
    init.body = request.body;
    init.duplex = "half";
  }

  try {
    return responseFromBackend(await fetcher(target, init), access);
  } catch {
    return jsonResponse(502, "Internal API is unavailable.");
  }
}
