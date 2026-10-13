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

const REQUEST_HEADERS = [
  "accept",
  "accept-language",
  "content-type",
  "if-match",
  "if-none-match",
  "origin",
  "range",
];
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

function jsonResponse(
  status: number,
  detail: string,
  challenge = false,
): Response {
  const headers = new Headers({
    "Content-Type": "application/json",
    "Cache-Control": "no-store",
  });
  if (challenge)
    headers.set(
      "WWW-Authenticate",
      'Basic realm="Terex Admin", charset="UTF-8"',
    );
  return new Response(JSON.stringify({ detail }), { status, headers });
}

function accessFailure(
  access: AdminAccess,
  config: AdminAccessConfig,
): Response {
  return jsonResponse(
    accessStatus(access, config),
    accessDetail(access),
    requiresBasicChallenge(access, config),
  );
}

// The destination comes only from runtime configuration. The caller supplies
// path segments, which are encoded one-by-one so a decoded ID cannot escape /api.
export function backendTarget(
  backendURL: string | undefined,
  segments: readonly string[],
  search: string,
): URL | null {
  if (
    !backendURL ||
    backendURL.length > 2048 ||
    (search !== "" && !search.startsWith("?"))
  )
    return null;
  try {
    const base = new URL(backendURL);
    if (
      (base.protocol !== "http:" && base.protocol !== "https:") ||
      !base.host ||
      base.username ||
      base.password ||
      base.pathname !== "/" ||
      base.search ||
      base.hash ||
      segments.length === 0
    )
      return null;
    const encoded = segments.map((segment) => {
      if (
        !segment ||
        segment === "." ||
        segment === ".." ||
        /[\u0000\r\n]/.test(segment)
      )
        throw new Error("unsafe path segment");
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

function forwardedHeaders(
  input: Headers,
  access: AdminAccess,
  config: AdminAccessConfig,
): Headers {
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
    headers.set(
      "Authorization",
      `Basic ${Buffer.from(credentials, "utf8").toString("base64")}`,
    );
  } else if (access === "service") {
    // adminAccess permits this only for the narrow collector routes. Preserve
    // the unverified token for main-be to validate; never replace it with Basic.
    headers.set("Authorization", input.get("authorization")!);
  }
  return headers;
}

function responseFromBackend(
  response: Response,
  access: AdminAccess,
): Response {
  // A 401 after the proxy attached its own Basic credential is an internal
  // configuration failure. A collector Bearer response stays visible to the
  // caller, but its WWW-Authenticate challenge is still never relayed.
  if (response.status === 401 && access === "operator")
    return jsonResponse(502, "Internal API authentication failed.");
  const headers = new Headers();
  response.headers.forEach((value, name) => {
    if (!HOP_BY_HOP_RESPONSE_HEADERS.has(name.toLowerCase()))
      headers.append(name, value);
  });
  return new Response(response.body, {
    status: response.status,
    statusText: response.statusText,
    headers,
  });
}

export async function proxyAPIRequest(
  request: Request,
  segments: readonly string[],
  config: BackendProxyConfig,
  fetcher: Fetcher = fetch,
): Promise<Response> {
  const inbound = new URL(request.url);
  const access = adminAccess(
    inbound.pathname,
    request.method,
    request.headers,
    config.auth,
  );
  if (
    access !== "operator" &&
    access !== "service" &&
    access !== "oauth_callback" &&
    access !== "pairing_payload"
  )
    return accessFailure(access, config.auth);

  const origin =
    access === "pairing_payload" ? request.headers.get("origin") : null;
  const cors = (response: Response) => {
    if (origin) {
      response.headers.set("Access-Control-Allow-Origin", origin);
      response.headers.set("Vary", "Origin");
    }
    response.headers.set("Cache-Control", "no-store");
    return response;
  };
  if (access === "pairing_payload" && request.method === "OPTIONS") {
    const response = new Response(null, {
      status: 204,
      headers: {
        "Access-Control-Allow-Methods": "POST",
        "Access-Control-Allow-Headers": "Content-Type",
        "Access-Control-Max-Age": "600",
      },
    });
    return cors(response);
  }

  const target = backendTarget(config.backendURL, segments, inbound.search);
  if (!target) {
    const response = jsonResponse(503, "Backend API is not configured.");
    return access === "pairing_payload" ? cors(response) : response;
  }

  const init: RequestInit & { duplex?: "half" } = {
    method: request.method,
    headers: forwardedHeaders(request.headers, access, config.auth),
    redirect: "manual",
    cache: "no-store",
  };
  if (request.method !== "GET" && request.method !== "HEAD" && request.body) {
    if (access === "pairing_payload") {
      if (
        !(request.headers.get("content-type") || "")
          .toLowerCase()
          .startsWith("application/json")
      )
        return cors(jsonResponse(415, "Pairing payload must be JSON."));
      const reader = request.body.getReader();
      const chunks: Uint8Array[] = [];
      let size = 0;
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        size += value.byteLength;
        if (size > 1024 * 1024) {
          await reader.cancel();
          return cors(jsonResponse(413, "Pairing payload is too large."));
        }
        chunks.push(value);
      }
      const body = new Uint8Array(size);
      let position = 0;
      for (const chunk of chunks) {
        body.set(chunk, position);
        position += chunk.length;
      }
      init.body = body;
    } else init.body = request.body;
    init.duplex = "half";
  }

  try {
    const response = responseFromBackend(await fetcher(target, init), access);
    return access === "pairing_payload" ? cors(response) : response;
  } catch {
    const response = jsonResponse(502, "Internal API is unavailable.");
    return access === "pairing_payload" ? cors(response) : response;
  }
}
