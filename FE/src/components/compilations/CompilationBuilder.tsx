"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useMemo, useState } from "react";
import ConfirmAction from "@/components/ui/ConfirmAction";
import { createCompilation, fetchCompilation, fetchImports, fetchTwitchSettings, saveCompilation } from "@/lib/api";
import { eligibleImports, moveCompilationItem, validateCompilation } from "@/lib/twitch";
import type { Compilation, CompilationItem, ImportedClip, TwitchSettings } from "@/types/twitch";

const input = "w-full min-w-0 rounded-md border border-slate-600 bg-slate-700 px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500";
const button = "rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600 disabled:opacity-40";
const panel = "rounded-xl border border-slate-700 bg-slate-800 p-4 sm:p-5";
const messageOf = (failure: unknown) => failure instanceof Error ? failure.message : String(failure);
const signature = (title: string, items: CompilationItem[]) => JSON.stringify({ title, items });

export default function CompilationBuilder({ clipID }: { clipID?: string }) {
  const router = useRouter();
  const [saved, setSaved] = useState<Compilation | null>(null);
  const [title, setTitle] = useState("");
  const [items, setItems] = useState<CompilationItem[]>([]);
  const [imports, setImports] = useState<ImportedClip[]>([]);
  const [settings, setSettings] = useState<TwitchSettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [candidateID, setCandidateID] = useState("");
  const [discardAction, setDiscardAction] = useState<"reload" | "leave" | null>(null);
  const eligible = useMemo(() => eligibleImports(imports), [imports]);
  const sources = useMemo(() => new Map(imports.map((item) => [item.clip_id, item])), [imports]);
  const candidates = eligible.filter((item) => !items.some((selected) => selected.clip_id === item.clip_id));
  const dirty = saved ? signature(title, items) !== signature(saved.title, saved.items) : !!title || items.length > 0;
  const duration = (source?: ImportedClip) => source?.duration_sec || source?.source.duration_sec || 0;

  const load = useCallback(async () => {
    setLoading(true); setError("");
    try {
      const [available, limits, existing] = await Promise.all([
        fetchImports("ready"), fetchTwitchSettings(), clipID ? fetchCompilation(clipID) : Promise.resolve(null),
      ]);
      setImports(available ?? []); setSettings(limits); setSaved(existing);
      if (existing) { setTitle(existing.title); setItems(existing.items ?? []); }
      else { setTitle(""); setItems([]); }
    } catch (failure) { setError(messageOf(failure)); }
    finally { setLoading(false); }
  }, [clipID]);
  useEffect(() => { void load(); }, [load]);
  useEffect(() => {
    if (!dirty) return;
    const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);
  useEffect(() => {
    if (!clipID || !saved || !["pending", "queued", "rendering"].includes(saved.state)) return;
    let active = true;
    const timer = setInterval(() => {
      fetchCompilation(clipID).then((latest) => {
        if (!active) return;
        if (latest.version !== saved.version) { setError("This compilation changed elsewhere. Save or reload to resolve the version conflict."); return; }
        setSaved(latest);
      }).catch((failure) => { if (active) setError(messageOf(failure)); });
    }, 5000);
    return () => { active = false; clearInterval(timer); };
  }, [clipID, saved?.state, saved?.version]);

  function add() {
    const source = candidates.find((item) => item.import_id === candidateID);
    if (!source?.clip_id || !source.revision_id || duration(source) <= 0) return;
    setItems((previous) => [...previous, { clip_id: source.clip_id!, revision_id: source.revision_id!, start_sec: 0, end_sec: duration(source) }]);
    setCandidateID("");
  }
  async function save() {
    if (!settings) return;
    setError(""); setNotice("");
    const issue = validateCompilation(title, items, settings.compilation_min_clips, settings.compilation_max_clips);
    if (issue) { setError(issue); return; }
    if (items.some((item) => {
      const source = eligible.find((candidate) => candidate.clip_id === item.clip_id);
      return !source || item.end_sec > duration(source);
    })) { setError("Every selected source must still be ready, not rejected, and cover the selected bounds. Reload sources if their state changed."); return; }
    setBusy(true);
    try {
      const spec = { title: title.trim(), items };
      const result = clipID && saved ? await saveCompilation(clipID, saved.version, spec) : await createCompilation(spec);
      setSaved(result); setTitle(result.title); setItems(result.items ?? []);
      setNotice("Compilation queued for rendering. Review its preview in the editor before approval.");
      if (!clipID) router.replace(`/admin/compilations/${encodeURIComponent(result.clip_id)}`);
    } catch (failure) { setError(messageOf(failure)); }
    finally { setBusy(false); }
  }

  return <div className="flex min-h-full flex-col gap-5 p-4 sm:p-6">
    <div className="flex flex-wrap items-center justify-between gap-3">
      <h1 className="text-xl font-semibold">{clipID ? "Edit compilation" : "New compilation"}</h1>
      <Link href="/admin/compilations" className="text-sm text-indigo-400" onClick={(event) => { if (dirty) { event.preventDefault(); setDiscardAction("leave"); } }}>All compilations →</Link>
    </div>
    <p className="text-sm text-slate-400">Arrange imported clips and source bounds. A transition is inserted between clips. Content rights require a separate review.</p>
    {error && <p role="alert" className="text-sm text-red-400">{error}</p>}
    {notice && <p role="status" className="text-sm text-emerald-400">{notice}</p>}
    {loading && <p role="status" className="text-sm text-slate-400">Loading sources and compilation…</p>}
    <section className={panel} aria-labelledby="compilation-title">
      <form className="flex flex-col gap-4" onSubmit={(event) => { event.preventDefault(); void save(); }}>
        <label id="compilation-title" className="flex flex-col gap-1 text-xs text-slate-400">Compilation title
          <input className={input} required maxLength={100} value={title} disabled={loading || busy || !settings}
            onChange={(event) => setTitle(event.target.value)} />
        </label>
        <div className="flex flex-col gap-2 sm:flex-row">
          <select aria-label="Ready imported clip to add" className={input} value={candidateID} disabled={loading || busy || !settings}
            onChange={(event) => setCandidateID(event.target.value)}>
            <option value="">Select an imported clip</option>
            {candidates.map((source) => <option key={source.import_id} value={source.import_id}>{source.source.broadcaster_login} · {source.source.title || source.source.clip_id} · {duration(source).toFixed(1)}s</option>)}
          </select>
          <button type="button" className={`${button} shrink-0`} disabled={!candidateID || busy || items.length >= (settings?.compilation_max_clips ?? 10)} onClick={add}>Add clip</button>
        </div>
        <p className="text-xs text-slate-400">{settings ? `Choose ${settings.compilation_min_clips}–${settings.compilation_max_clips} ready clips. ` : ""}Only completed, non-rejected imports can be added. Rights are not verified.</p>
        {!items.length && <p className="text-sm text-slate-500">Add clips to build the compilation.</p>}
        <ol className="flex flex-col gap-3">{items.map((item, index) => {
          const source = sources.get(item.clip_id);
          return <li key={item.clip_id} className="rounded-lg border border-slate-700 p-3">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="min-w-0"><p className="break-words text-sm font-medium">{index + 1}. {source?.source.title || item.clip_id}</p><p className="mt-1 text-xs text-slate-400">{source?.source.broadcaster_login || "Source unavailable"} · revision {item.revision_id} · rights not verified</p></div>
              <div className="flex gap-2">
                <button type="button" className={button} aria-label={`Move clip ${index + 1} up`} disabled={busy || index === 0} onClick={() => setItems((previous) => moveCompilationItem(previous, index, -1))}>↑</button>
                <button type="button" className={button} aria-label={`Move clip ${index + 1} down`} disabled={busy || index === items.length - 1} onClick={() => setItems((previous) => moveCompilationItem(previous, index, 1))}>↓</button>
                <button type="button" className={`${button} text-red-300`} aria-label={`Remove clip ${index + 1}`} disabled={busy} onClick={() => setItems((previous) => previous.filter((_, selected) => selected !== index))}>Remove</button>
              </div>
            </div>
            <div className="mt-3 grid gap-3 sm:grid-cols-2">
              {(["start_sec", "end_sec"] as const).map((bound) => <label key={bound} className="flex flex-col gap-1 text-xs text-slate-400">{bound === "start_sec" ? "Source start (seconds)" : "Source end (seconds)"}
                <input className={input} type="number" required min={0} max={source ? duration(source) : undefined} step="any" disabled={busy}
                  value={Number.isFinite(item[bound]) ? item[bound] : ""} onChange={(event) => {
                    const value = event.target.value === "" ? NaN : Number(event.target.value);
                    setItems((previous) => previous.map((selected, selectedIndex) => selectedIndex === index ? { ...selected, [bound]: value } : selected));
                  }} />
              </label>)}
            </div>
          </li>;
        })}</ol>
        <div className="flex flex-wrap items-center gap-3">
          <button className="rounded-lg bg-indigo-600 px-4 py-2 text-sm hover:bg-indigo-500 disabled:opacity-40" disabled={busy || loading || !settings || (clipID ? !saved || !dirty : false)}>{busy ? "Saving…" : clipID ? "Save & render" : "Create & render"}</button>
          <button type="button" className={button} disabled={busy || loading} onClick={() => { if (dirty) setDiscardAction("reload"); else void load(); }}>Refresh</button>
          {dirty && <span className="text-xs text-amber-300">Unsaved changes</span>}
        </div>
      </form>
    </section>
    {saved && <section className={panel} aria-labelledby="preview-title">
      <h2 id="preview-title" className="font-semibold">Preview & review</h2>
      <p className="mt-2 text-sm text-slate-400">Render: {saved.state} · revision {saved.revision_id} · version {saved.version} · {saved.approved_at ? "Approved" : "Approval required"}</p>
      {saved.error && <p role="alert" className="mt-2 text-sm text-red-400">{saved.error}</p>}
      {saved.state === "ready" && saved.preview_url ? <video className="mt-4 max-h-[60vh] w-full rounded-lg bg-black" controls preload="metadata" src={saved.preview_url} /> : <p className="mt-3 text-sm text-slate-500">A preview appears after rendering completes.</p>}
      <div className="mt-4 flex flex-wrap gap-4 text-sm"><Link href={`/admin/clips/${encodeURIComponent(saved.clip_id)}`} className="text-indigo-400">Open editor for metadata, review & approval →</Link><Link href="/admin/calendar" className="text-indigo-400">Publication calendar →</Link></div>
      <p className="mt-3 text-xs text-slate-400">Changing the compilation creates a new render and requires approval again. Saving here does not publish it.</p>
    </section>}
    {discardAction && <ConfirmAction title="Discard unsaved changes" message="The current title, source bounds and clip order have not been saved. Discard them?" confirmLabel="Discard" onCancel={() => setDiscardAction(null)} onConfirm={() => { const next = discardAction; setDiscardAction(null); if (next === "reload") void load(); else router.push("/admin/compilations"); }} />}
  </div>;
}
