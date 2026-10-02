"use client";

import { useCallback, useEffect, useState } from "react";
import { approveClip, rejectClip, fetchClips } from "@/lib/api";
import type { Clip } from "@/types";

const TABS = [
  { key: "pending", label: "Pending approval" },
  { key: "approved", label: "Approved" },
  { key: "rejected", label: "Rejected" },
] as const;

function fmt(sec: number) {
  const m = Math.floor(sec / 60);
  const s = Math.floor(sec % 60);
  return `${m}:${String(s).padStart(2, "0")}`;
}

export default function ClipsPage() {
  const [tab, setTab] = useState<(typeof TABS)[number]["key"]>("pending");
  const [clips, setClips] = useState<Clip[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try { setClips(await fetchClips(tab)); }
    catch (e) { setError(e instanceof Error ? e.message : "Failed to load clips"); }
    finally { setLoading(false); }
  }, [tab]);

  useEffect(() => { load(); }, [load]);

  async function act(c: Clip, fn: (id: string) => Promise<void>) {
    try {
      await fn(c.clip_id);
      setClips((prev) => prev.filter((x) => x.clip_id !== c.clip_id));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Action failed");
    }
  }

  return (
    <div className="p-6 flex flex-col gap-6 min-h-full">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-slate-100">Clips</h1>
        <button onClick={load} disabled={loading}
          className="bg-slate-700 hover:bg-slate-600 disabled:opacity-40 text-white text-sm font-medium px-4 py-2 rounded-lg border border-slate-600 transition-colors">
          {loading ? "Loading…" : "Refresh"}
        </button>
      </div>

      <div className="flex gap-2">
        {TABS.map((t) => (
          <button key={t.key} onClick={() => setTab(t.key)}
            className={`text-sm px-3 py-1.5 rounded-lg border transition-colors ${tab === t.key ? "bg-indigo-600 border-indigo-500 text-white" : "bg-slate-800 border-slate-700 text-slate-300 hover:bg-slate-700"}`}>
            {t.label}
          </button>
        ))}
      </div>

      {error && <p className="text-sm text-red-400">{error}</p>}
      {!loading && clips.length === 0 && <p className="text-slate-500 text-sm">No clips here.</p>}

      <div className="grid gap-4 grid-cols-1 md:grid-cols-2 xl:grid-cols-3">
        {clips.map((c) => (
          <div key={c.clip_id} className="bg-slate-800 border border-slate-700 rounded-xl overflow-hidden flex flex-col">
            <div className="bg-black aspect-[9/16] max-h-96 flex items-center justify-center">
              {c.preview_url ? (
                <video src={c.preview_url} className="w-full h-full object-contain" controls preload="metadata" />
              ) : (
                <span className="text-xs text-slate-600">no preview</span>
              )}
            </div>
            <div className="p-3 flex flex-col gap-2">
              <div className="flex flex-col gap-0.5">
                <span className="text-sm font-medium text-slate-100 truncate">{c.title || c.clip_id}</span>
                <span className="text-xs text-slate-500 font-mono">job {c.job_id} · {fmt(c.start_sec)}–{fmt(c.end_sec)}</span>
                {c.hashtags?.length > 0 && <span className="text-xs text-slate-400">{c.hashtags.map((h) => `#${h.replace(/^#/, "")}`).join(" ")}</span>}
              </div>
              {c.approval === "pending" && (
                <div className="flex gap-2">
                  <button onClick={() => act(c, approveClip)} className="flex-1 bg-emerald-600 hover:bg-emerald-500 text-white text-sm font-medium px-3 py-1.5 rounded-lg transition-colors">Approve</button>
                  <button onClick={() => act(c, rejectClip)} className="flex-1 bg-slate-700 hover:bg-slate-600 text-red-300 text-sm font-medium px-3 py-1.5 rounded-lg border border-slate-600 transition-colors">Reject</button>
                </div>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
