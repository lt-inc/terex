"use client";

import { useState } from "react";
import MultiSelect from "@/components/ui/MultiSelect";
import { updateSubJob, deleteSubJob } from "@/lib/api";
import type { SubJobData, AccountsResponse } from "@/types";

interface SubJobCardProps {
  subJob: SubJobData;
  accounts: AccountsResponse;
  bannerOptions: string[];
  onUpdated: (updated: SubJobData) => void;
  onDeleted: () => void;
}

const PLATFORMS = ["youtube", "tiktok", "instagram"];
const STATUS_COLORS: Record<string, string> = {
  youtube: "bg-red-900/40 text-red-300",
  tiktok: "bg-slate-700 text-slate-300",
  instagram: "bg-purple-900/40 text-purple-300",
};

export default function SubJobCard({ subJob, accounts, bannerOptions, onUpdated, onDeleted }: SubJobCardProps) {
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);

  const [platform, setPlatform] = useState(subJob.platform);
  const [accountNames, setAccountNames] = useState<string[]>(subJob.account_names);
  const [bannerName, setBannerName] = useState(subJob.banner_name ?? "");
  const [pubDate, setPubDate] = useState(subJob.publish_datetime?.slice(0, 10) ?? "");
  const [pubTime, setPubTime] = useState(subJob.publish_datetime?.slice(11, 16) ?? "");

  function resetEdit() {
    setPlatform(subJob.platform);
    setAccountNames(subJob.account_names);
    setBannerName(subJob.banner_name ?? "");
    setPubDate(subJob.publish_datetime?.slice(0, 10) ?? "");
    setPubTime(subJob.publish_datetime?.slice(11, 16) ?? "");
    setEditing(false);
  }

  async function handleSave() {
    setSaving(true);
    try {
      const publish_datetime = pubDate && pubTime ? `${pubDate}T${pubTime}:00Z` : null;
      const updated = await updateSubJob(subJob.sub_job_id, {
        platform,
        account_names: accountNames,
        banner_name: bannerName || null,
        publish_datetime,
      });
      onUpdated(updated);
      setEditing(false);
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!confirm(`Delete sub_job ${subJob.sub_job_id}?`)) return;
    await deleteSubJob(subJob.sub_job_id);
    onDeleted();
  }

  const platformAccounts = accounts[platform as keyof AccountsResponse] ?? [];

  return (
    <div className="bg-slate-900 border border-slate-700 rounded-lg overflow-hidden">
      {/* Header */}
      <div className="flex items-center gap-3 px-4 py-2.5">
        <button onClick={() => setOpen((o) => !o)} className="flex items-center gap-2 flex-1 min-w-0 text-left">
          <svg className={`w-3.5 h-3.5 shrink-0 text-slate-500 transition-transform ${open ? "rotate-90" : ""}`} fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
          </svg>
          <span className={`text-xs font-medium px-2 py-0.5 rounded ${STATUS_COLORS[platform] ?? "bg-slate-700 text-slate-300"}`}>
            {platform}
          </span>
          <span className="text-xs text-slate-400 font-mono truncate">{subJob.sub_job_id}</span>
          {subJob.publish_datetime && (
            <span className="text-xs text-slate-500 ml-auto shrink-0">
              {new Date(subJob.publish_datetime).toLocaleString()}
            </span>
          )}
        </button>
        <button onClick={handleDelete} className="text-slate-500 hover:text-red-400 transition-colors text-lg leading-none px-1">×</button>
      </div>

      {/* Body */}
      {open && (
        <div className="px-4 pb-4 flex flex-col gap-3 border-t border-slate-700 pt-3">
          {!editing ? (
            <>
              <div className="grid grid-cols-2 gap-x-6 gap-y-1 text-sm">
                <Row label="Platform" value={subJob.platform} />
                <Row label="Accounts" value={subJob.account_names.join(", ") || "—"} />
                <Row label="Banner" value={subJob.banner_name ?? "—"} />
                <Row label="Publish" value={subJob.publish_datetime ? new Date(subJob.publish_datetime).toLocaleString() : "—"} />
              </div>
              <button onClick={() => setEditing(true)} className="self-start text-xs text-indigo-400 hover:text-indigo-300 transition-colors">
                Edit
              </button>
            </>
          ) : (
            <div className="flex flex-col gap-3">
              <div className="flex flex-col gap-1">
                <label className="text-xs text-slate-400">Platform</label>
                <select value={platform} onChange={(e) => { setPlatform(e.target.value); setAccountNames([]); }}
                  className="bg-slate-700 border border-slate-600 rounded-md px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500">
                  {PLATFORMS.map((p) => <option key={p} value={p}>{p}</option>)}
                </select>
              </div>
              <div className="flex flex-col gap-1">
                <label className="text-xs text-slate-400">Accounts</label>
                <MultiSelect label="" options={platformAccounts} selected={accountNames} onChange={setAccountNames} />
              </div>
              <div className="flex flex-col gap-1">
                <label className="text-xs text-slate-400">Banner</label>
                <select value={bannerName} onChange={(e) => setBannerName(e.target.value)}
                  className="bg-slate-700 border border-slate-600 rounded-md px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500">
                  <option value="">— none —</option>
                  {bannerOptions.map((b) => <option key={b} value={b}>{b}</option>)}
                </select>
              </div>
              <div className="flex gap-2">
                <div className="flex flex-col gap-1 flex-1">
                  <label className="text-xs text-slate-400">Date</label>
                  <input type="date" value={pubDate} onChange={(e) => setPubDate(e.target.value)}
                    className="bg-slate-700 border border-slate-600 rounded-md px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500 [color-scheme:dark]" />
                </div>
                <div className="flex flex-col gap-1 flex-1">
                  <label className="text-xs text-slate-400">Time</label>
                  <input type="time" value={pubTime} onChange={(e) => setPubTime(e.target.value)}
                    className="bg-slate-700 border border-slate-600 rounded-md px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500 [color-scheme:dark]" />
                </div>
              </div>
              <div className="flex gap-2">
                <button onClick={handleSave} disabled={saving}
                  className="bg-indigo-600 hover:bg-indigo-500 disabled:opacity-40 text-white text-xs font-medium px-3 py-1.5 rounded-md transition-colors">
                  {saving ? "Saving…" : "Save"}
                </button>
                <button onClick={resetEdit} className="text-xs text-slate-400 hover:text-slate-200 transition-colors px-3 py-1.5">
                  Cancel
                </button>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <>
      <span className="text-slate-500">{label}</span>
      <span className="text-slate-200">{value}</span>
    </>
  );
}
