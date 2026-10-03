"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { fetchCompilations, toUTC7, triggerAutoCompilation } from "@/lib/api";
import type { Compilation } from "@/types/twitch";

const button = "rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600 disabled:opacity-40";
const messageOf = (failure: unknown) => failure instanceof Error ? failure.message : String(failure);

export default function CompilationsPage() {
  const [compilations, setCompilations] = useState<Compilation[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const load = useCallback(async () => {
    setLoading(true); setError("");
    try { setCompilations((await fetchCompilations()) ?? []); }
    catch (failure) { setError(messageOf(failure)); }
    finally { setLoading(false); }
  }, []);
  useEffect(() => { void load(); }, [load]);
  async function automatic() {
    setBusy(true); setError(""); setNotice("");
    try {
      const result = await triggerAutoCompilation();
      if (result.created && result.compilation) {
        const compilation = result.compilation;
        setCompilations((previous) => [compilation, ...previous.filter((item) => item.clip_id !== compilation.clip_id)]);
        setNotice("Compilation prepared for review. Rendering and approval are still required.");
      } else setNotice("No compilation created. The daily limit or eligible source requirements have not been met.");
    } catch (failure) { setError(messageOf(failure)); }
    finally { setBusy(false); }
  }
  return <div className="flex min-h-full flex-col gap-5 p-4 sm:p-6">
    <div className="flex flex-wrap items-center justify-between gap-3">
      <h1 className="text-xl font-semibold">Compilations</h1>
      <div className="flex flex-wrap gap-2">
        <button className={button} disabled={loading || busy} onClick={() => void load()}>{loading ? "Refreshing…" : "Refresh"}</button>
        <button className={button} disabled={busy || loading} onClick={() => void automatic()}>{busy ? "Preparing…" : "Prepare automatically"}</button>
        <Link href="/admin/compilations/new" className="rounded-lg bg-indigo-600 px-3 py-2 text-sm hover:bg-indigo-500">Build manually</Link>
      </div>
    </div>
    <p className="text-sm text-slate-400">Combine ready imported clips with transitions. Automatic selection respects the configured daily and clip limits; publication requires review in the editor.</p>
    {error && <p role="alert" className="text-sm text-red-400">{error}</p>}
    {notice && <p role="status" className="text-sm text-emerald-400">{notice}</p>}
    {!loading && !compilations.length && <p className="text-sm text-slate-500">No compilations yet. <Link href="/admin/twitch" className="text-indigo-400">Add Twitch sources →</Link></p>}
    <div className="grid gap-4 lg:grid-cols-2">{compilations.map((item) => <article key={item.clip_id} className="min-w-0 rounded-xl border border-slate-700 bg-slate-800 p-4">
      <h2 className="break-words font-semibold">{item.title || item.clip_id}</h2>
      <p className="mt-2 text-sm text-slate-400">{item.items?.length ?? 0} clips · {item.state} · version {item.version} · {item.approved_at ? "Approved" : "Approval required"}</p>
      <p className="mt-1 text-xs text-slate-500">{toUTC7(item.created_at)} · Asia/Tomsk · rights not verified</p>
      {item.error && <p role="alert" className="mt-2 text-sm text-red-400">{item.error}</p>}
      <div className="mt-4 flex flex-wrap gap-4 text-sm"><Link href={`/admin/compilations/${encodeURIComponent(item.clip_id)}`} className="text-indigo-400">Edit order & preview →</Link><Link href={`/admin/clips/${encodeURIComponent(item.clip_id)}`} className="text-indigo-400">Review & approve →</Link></div>
    </article>)}</div>
  </div>;
}
