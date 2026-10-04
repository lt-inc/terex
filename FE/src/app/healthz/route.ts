// Outside the administrator middleware matcher so probes need no credentials.
export function GET() {
  return Response.json({ status: "ok" }, { headers: { "Cache-Control": "no-store" } });
}
