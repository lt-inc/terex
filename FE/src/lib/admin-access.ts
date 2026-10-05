// Server and middleware access policy. Do not import this module into client components.
export const TRAEFIK_AUTHENTICATED_USER_HEADER = "x-terex-authenticated-user";
export const OAUTH_CALLBACK_PATH = "/api/accounts/oauth/callback";

export interface HeaderReader {
  get(name: string): string | null;
}

export interface AdminAccessConfig {
  mode?: string;
  username?: string;
  password?: string;
  publicURL?: string;
}

export type AuthMode = "basic" | "traefik";
export type AdminAccess = "operator" | "service" | "oauth_callback" | "authenticate" | "unconfigured" | "forbidden";

export function authMode(config: AdminAccessConfig): AuthMode | null {
  const value = config.mode ?? "basic";
  if (value === "" || value === "basic") return "basic";
  if (value === "traefik") return "traefik";
  return null;
}

export function serviceBearerRoute(pathname: string, method: string): boolean {
  return (pathname === "/api/twitch/settings" && method === "GET")
    || (pathname === "/api/imports" && ["GET", "POST"].includes(method))
    || (/^\/api\/imports\/[^/]+$/.test(pathname) && method === "GET")
    || (/^\/api\/imports\/[^/]+\/complete$/.test(pathname) && method === "POST");
}

function equalSecret(actual: string, expected: string): boolean {
  let difference = actual.length ^ expected.length;
  for (let index = 0; index < Math.max(actual.length, expected.length); index++) {
    difference |= (actual.charCodeAt(index) || 0) ^ (expected.charCodeAt(index) || 0);
  }
  return difference === 0;
}

export function basicCredentials(header: string | null): [string, string] | null {
  if (!header || header.length > 4096 || !/^Basic [A-Za-z0-9+/]+={0,2}$/i.test(header)) return null;
  try {
    const decoded = new TextDecoder("utf-8", { fatal: true }).decode(
      Uint8Array.from(atob(header.slice(6)), (character) => character.charCodeAt(0)),
    );
    const separator = decoded.indexOf(":");
    return separator >= 0 ? [decoded.slice(0, separator), decoded.slice(separator + 1)] : null;
  } catch {
    return null;
  }
}

export function publicOrigin(publicURL: string | undefined): string | null {
  if (!publicURL) return null;
  try {
    const parsed = new URL(publicURL);
    if (
      (parsed.protocol !== "http:" && parsed.protocol !== "https:")
      || !parsed.host
      || parsed.username
      || parsed.password
      || parsed.search
      || parsed.hash
      || parsed.pathname !== "/"
    ) return null;
    return parsed.origin;
  } catch {
    return null;
  }
}

function hasCompleteOperatorCredentials(config: AdminAccessConfig): boolean {
  return !!config.username && !!config.password;
}

function hasTraefikMarker(headers: HeaderReader): boolean {
  const marker = headers.get(TRAEFIK_AUTHENTICATED_USER_HEADER);
  return !!marker && marker.length <= 256 && marker.trim() !== "";
}

function isUnsafe(method: string): boolean {
  return method !== "GET" && method !== "HEAD" && method !== "OPTIONS";
}

function originIsAllowed(headers: HeaderReader, expectedOrigin: string | null): boolean {
  const origin = headers.get("origin");
  if (!origin) return true;
  try {
    const parsed = new URL(origin);
    const host = headers.get("host");
    if (!host || parsed.origin !== origin || parsed.host.toLowerCase() !== host.toLowerCase()) return false;
    return !expectedOrigin || parsed.origin === expectedOrigin;
  } catch {
    return false;
  }
}

function isBearer(header: string | null): boolean {
  return !!header && /^Bearer \S+$/i.test(header);
}

function startsBearer(header: string | null): boolean {
  return !!header && /^Bearer(?:\s|$)/i.test(header);
}

export function isExactOAuthCallback(pathname: string, method: string): boolean {
  return pathname === OAUTH_CALLBACK_PATH && method === "GET";
}

// The result tells both the Edge middleware and the Node API proxy how a request
// may proceed. A Bearer token is never converted into operator credentials.
export function adminAccess(
  pathname: string,
  method: string,
  headers: HeaderReader,
  config: AdminAccessConfig,
): AdminAccess {
  // The callback is protected by its short-lived, one-use OAuth state in main-be.
  if (isExactOAuthCallback(pathname, method)) return "oauth_callback";

  const mode = authMode(config);
  const expectedOrigin = publicOrigin(config.publicURL);
  if (!mode || !hasCompleteOperatorCredentials(config)) return "unconfigured";
  if ((config.publicURL !== undefined && config.publicURL !== "" && !expectedOrigin) || (mode === "traefik" && !expectedOrigin)) {
    return "unconfigured";
  }
  if (isUnsafe(method) && !originIsAllowed(headers, expectedOrigin)) return "forbidden";

  // In staging the marker is set only after Traefik authentication. All browser
  // Authorization headers, including Bearer, are ignored by the API proxy.
  if (mode === "traefik") return hasTraefikMarker(headers) ? "operator" : "authenticate";

  const authorization = headers.get("authorization");
  if (startsBearer(authorization)) {
    if (!isBearer(authorization) || !serviceBearerRoute(pathname, method)) return "authenticate";
    return "service";
  }

  const credentials = basicCredentials(authorization);
  if (!credentials) return "authenticate";
  const usernameMatches = equalSecret(credentials[0], config.username!);
  const passwordMatches = equalSecret(credentials[1], config.password!);
  return usernameMatches && passwordMatches ? "operator" : "authenticate";
}

export function accessStatus(access: AdminAccess, config: AdminAccessConfig): number {
  if (access === "unconfigured") return 503;
  if (access === "forbidden") return 403;
  return authMode(config) === "basic" ? 401 : 403;
}

export function accessDetail(access: AdminAccess): string {
  if (access === "unconfigured") return "Frontend authentication is not configured.";
  if (access === "forbidden") return "Cross-origin actions are not allowed.";
  return "Administrator authentication required.";
}

export function requiresBasicChallenge(access: AdminAccess, config: AdminAccessConfig): boolean {
  return access === "authenticate" && authMode(config) === "basic";
}
