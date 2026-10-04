// Server-side access policy. Do not import this module into client components.
export interface AdminAccessConfig {
  username?: string;
  password?: string;
  production: boolean;
}

export type AdminAccess = "allow" | "authenticate" | "unconfigured";

export function serviceBearerRoute(pathname: string, method: string): boolean {
  return (pathname === "/api/twitch/settings" && method === "GET")
    || (pathname === "/api/imports" && ["GET", "POST"].includes(method))
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

export function adminAccess(
  pathname: string,
  method: string,
  authorization: string | null,
  config: AdminAccessConfig,
): AdminAccess {
  // This callback has its own short-lived, one-use OAuth state validation.
  if (pathname === "/api/accounts/oauth/callback" && method === "GET") return "allow";
  // The backend verifies TEREX_SERVICE_TOKEN. Never turn Bearer access into an
  // administrator session, or permit it on publishing/approval/settings writes.
  if (authorization && /^Bearer \S+$/i.test(authorization) && serviceBearerRoute(pathname, method)) return "allow";
  const configured = !!(config.username && config.password);
  if (!configured) {
    return config.production || !!(config.username || config.password) ? "unconfigured" : "allow";
  }
  const credentials = basicCredentials(authorization);
  if (!credentials) return "authenticate";
  const usernameMatches = equalSecret(credentials[0], config.username!);
  const passwordMatches = equalSecret(credentials[1], config.password!);
  return usernameMatches && passwordMatches ? "allow" : "authenticate";
}
