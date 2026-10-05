import { NextRequest, NextResponse } from "next/server";
import { accessDetail, accessStatus, adminAccess, requiresBasicChallenge, type AdminAccessConfig } from "./lib/admin-access";

function runtimeConfig(): AdminAccessConfig {
  const environment = process.env;
  return {
    mode: environment["FE_AUTH_MODE"],
    username: environment["ADMIN_USERNAME"],
    password: environment["ADMIN_PASSWORD"],
    publicURL: environment["APP_PUBLIC_URL"],
  };
}

export function middleware(request: NextRequest) {
  const credentials = runtimeConfig();
  const access = adminAccess(request.nextUrl.pathname, request.method, request.headers, credentials);
  if (access === "operator" || access === "service" || access === "oauth_callback") return NextResponse.next();
  const isAPI = request.nextUrl.pathname.startsWith("/api/");
  const response = isAPI
    ? NextResponse.json({ detail: accessDetail(access) }, { status: accessStatus(access, credentials) })
    : new NextResponse(accessDetail(access), { status: accessStatus(access, credentials) });
  response.headers.set("Cache-Control", "no-store");
  if (requiresBasicChallenge(access, credentials)) response.headers.set("WWW-Authenticate", 'Basic realm="Terex Admin", charset="UTF-8"');
  return response;
}

export const config = { matcher: ["/", "/admin/:path*", "/api/:path*"] };
