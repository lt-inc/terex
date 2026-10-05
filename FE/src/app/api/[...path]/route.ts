import { proxyAPIRequest, type BackendProxyConfig } from "@/lib/backend-proxy";
import type { NextRequest } from "next/server";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

type Context = { params: Promise<{ path: string[] }> };

function environment(name: string): string | undefined {
  // Dynamic lookup keeps these values runtime-configurable in the production image.
  return process.env[name];
}

function runtimeConfig(): BackendProxyConfig {
  return {
    backendURL: environment("BACKEND_URL"),
    auth: {
      mode: environment("FE_AUTH_MODE"),
      username: environment("ADMIN_USERNAME"),
      password: environment("ADMIN_PASSWORD"),
      publicURL: environment("APP_PUBLIC_URL"),
    },
  };
}

async function handle(request: NextRequest, context: Context) {
  const { path } = await context.params;
  return proxyAPIRequest(request, path, runtimeConfig());
}

export { handle as GET, handle as HEAD, handle as POST, handle as PUT, handle as PATCH, handle as DELETE, handle as OPTIONS };
