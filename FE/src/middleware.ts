import { NextRequest, NextResponse } from "next/server";
import { adminAccess } from "./lib/admin-access";

export function middleware(request: NextRequest) {
  const origin = request.headers.get("origin");
  if (origin && !["GET", "HEAD", "OPTIONS"].includes(request.method)) {
    try {
      if (new URL(origin).host !== request.headers.get("host")) {
        return NextResponse.json({ detail: "Cross-origin actions are not allowed." }, { status: 403 });
      }
    } catch {
      return NextResponse.json({ detail: "Invalid request origin." }, { status: 403 });
    }
  }
  const result = adminAccess(
    request.nextUrl.pathname,
    request.method,
    request.headers.get("authorization"),
    {
      username: process.env.ADMIN_USERNAME,
      password: process.env.ADMIN_PASSWORD,
      production: process.env.NODE_ENV === "production",
    },
  );
  if (result === "allow") return NextResponse.next();
  const isAPI = request.nextUrl.pathname.startsWith("/api/");
  const message = result === "unconfigured"
    ? "Administrator access is not configured."
    : "Administrator authentication required.";
  const response = isAPI
    ? NextResponse.json({ detail: message }, { status: result === "unconfigured" ? 503 : 401 })
    : new NextResponse(message, { status: result === "unconfigured" ? 503 : 401 });
  response.headers.set("Cache-Control", "no-store");
  if (result === "authenticate") response.headers.set("WWW-Authenticate", 'Basic realm="Terex Admin", charset="UTF-8"');
  return response;
}

export const config = { matcher: ["/", "/admin/:path*", "/api/:path*"] };
