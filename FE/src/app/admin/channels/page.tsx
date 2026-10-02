"use client";

import { useCallback, useEffect, useState } from "react";
import Toggle from "@/components/ui/Toggle";
import MultiSelect from "@/components/ui/MultiSelect";
import { fetchChannels, createChannel, updateChannel, deleteChannel, fetchAccounts, toUTC7 } from "@/lib/api";
import type { AccountsResponse, ApproveMode, Channel, SliceMode } from "@/types";

const inputCls =
  "bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500";

export default function ChannelsPage() {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [accounts, setAccounts] = useState<AccountsResponse>({ youtube: [], tiktok: [], instagram: [] });
  const [loading, setLoading] = useState(true);
  const [toast, setToast] = useState<string | null>(null);

  // new channel form
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [approve, setApprove] = useState<ApproveMode>("by_approve");
  const [sliceMode, setSliceMode] = useState<SliceMode>("highlights");
  const [minDuration, setMinDuration] = useState(60);
  const [vertical, setVertical] = useState(true);
  const [subtitles, setSubtitles] = useState(true);
  const [shape, setShape] = useState<"circle" | "rectangle">("rectangle");
  const [ytAccounts, setYtAccounts] = useState<string[]>([]);
  const [step, setStep] = useState("60");
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [c, a] = await Promise.all([fetchChannels(), fetchAccounts()]);
      setChannels(c);
      setAccounts(a);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  function notify(msg: string) {
    setToast(msg);
    setTimeout(() => setToast(null), 3500);
  }

  async function handleAdd() {
    setSaving(true);
    try {
      await createChannel({
        name,
        url,
        approve_mode: approve,
        spec: {
          makeVertical: vertical,
          addSubtitles: subtitles,
          sliceMode,
          minVideoDuration: minDuration,
          shape,
          youtubeAccounts: ytAccounts,
          schedules: ytAccounts.length ? { youtube: { date: "", time: "", step } } : {},
        },
      });
      setName(""); setUrl("");
      notify("Channel added. Only videos published from now on will be processed.");
      await load();
    } catch (e) {
      notify(e instanceof Error ? e.message : "Failed to add channel");
    } finally {
      setSaving(false);
    }
  }

  async function patch(c: Channel, data: Parameters<typeof updateChannel>[1]) {
    const updated = await updateChannel(c.id, data);
    setChannels((prev) => prev.map((x) => (x.id === c.id ? updated : x)));
  }

  async function remove(c: Channel) {
    if (!confirm(`Stop monitoring "${c.name}"?`)) return;
    await deleteChannel(c.id);
    setChannels((prev) => prev.filter((x) => x.id !== c.id));
  }

  return (
    <div className="p-6 flex flex-col gap-6 min-h-full">
      <h1 className="text-xl font-semibold text-slate-100">Channel monitoring</h1>

      <div className="bg-slate-800 border border-slate-700 rounded-xl p-4 flex flex-col gap-3 max-w-2xl">
        <h2 className="text-sm font-medium text-slate-200">Add channel</h2>
        <input className={inputCls} placeholder="Name" value={name} onChange={(e) => setName(e.target.value)} />
        <input className={inputCls} placeholder="Channel URL (https://youtube.com/@handle or /channel/UC…)" value={url} onChange={(e) => setUrl(e.target.value)} />
        <div className="flex items-center gap-3">
          <span className="text-sm text-slate-300 flex-1">Publish</span>
          <select className={inputCls} value={approve} onChange={(e) => setApprove(e.target.value as ApproveMode)}>
            <option value="by_approve">By approve</option>
            <option value="auto_approve">Auto-approve</option>
          </select>
        </div>
        <div className="flex items-center gap-3">
          <span className="text-sm text-slate-300 flex-1">Montage mode</span>
          <select className={inputCls} value={sliceMode} onChange={(e) => setSliceMode(e.target.value as SliceMode)}>
            <option value="highlights">Highlights only</option>
            <option value="full">Full video (by intervals)</option>
          </select>
        </div>
        {sliceMode === "full" && (
          <div className="flex items-center gap-3">
            <span className="text-sm text-slate-300 flex-1">Min clip duration (sec)</span>
            <input type="number" min={1} className={`${inputCls} w-24`} value={minDuration} onChange={(e) => setMinDuration(Number(e.target.value))} />
          </div>
        )}
        <Toggle label="Make vertical?" checked={vertical} onChange={setVertical} />
        <Toggle label="Add subtitles?" checked={subtitles} onChange={setSubtitles} />
        <div className="flex items-center gap-3">
          <span className="text-sm text-slate-300 flex-1">Shape</span>
          <select className={inputCls} value={shape} onChange={(e) => setShape(e.target.value as "circle" | "rectangle")}>
            <option value="rectangle">Rectangle</option>
            <option value="circle">Circle</option>
          </select>
        </div>
        <MultiSelect label="YouTube accounts" options={accounts.youtube} selected={ytAccounts} onChange={setYtAccounts} />
        {ytAccounts.length > 0 && (
          <div className="flex items-center gap-3">
            <span className="text-sm text-slate-300 flex-1">Step between clips (min)</span>
            <input type="number" min={0} className={`${inputCls} w-24`} value={step} onChange={(e) => setStep(e.target.value)} />
          </div>
        )}
        <button onClick={handleAdd} disabled={saving || !url.trim()}
          className="self-start bg-indigo-600 hover:bg-indigo-500 disabled:opacity-40 text-white text-sm font-medium px-4 py-2 rounded-lg transition-colors">
          {saving ? "Adding…" : "Add channel"}
        </button>
      </div>

      {!loading && channels.length === 0 && <p className="text-slate-500 text-sm">No channels monitored yet.</p>}

      <div className="flex flex-col gap-3">
        {channels.map((c) => (
          <div key={c.id} className="bg-slate-800 border border-slate-700 rounded-xl p-4 flex items-center gap-4">
            <div className="flex-1 min-w-0 flex flex-col gap-0.5">
              <span className="text-sm font-medium text-slate-100 truncate">{c.name}</span>
              <a href={c.url} target="_blank" rel="noopener noreferrer" className="text-xs text-indigo-400 hover:underline truncate">{c.url}</a>
              <span className="text-xs text-slate-500">
                {c.spec.sliceMode === "full" ? "Full video" : "Highlights"} · last check:{" "}
                {c.last_checked_at ? toUTC7(c.last_checked_at) + " UTC+7" : "never"}
              </span>
            </div>
            <select className={inputCls} value={c.approve_mode} onChange={(e) => patch(c, { approve_mode: e.target.value as ApproveMode })}>
              <option value="by_approve">By approve</option>
              <option value="auto_approve">Auto-approve</option>
            </select>
            <div className="w-40">
              <Toggle label="Enabled" checked={c.enabled} onChange={(v) => patch(c, { enabled: v })} />
            </div>
            <button onClick={() => remove(c)} className="text-xs text-red-500 hover:text-red-400 transition-colors">Delete</button>
          </div>
        ))}
      </div>

      {toast && <div className="fixed bottom-6 right-6 bg-indigo-600 text-white text-sm px-4 py-3 rounded-lg shadow-lg z-50">{toast}</div>}
    </div>
  );
}
