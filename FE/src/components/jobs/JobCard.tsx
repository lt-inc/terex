"use client";

import { useState } from "react";
import Toggle from "@/components/ui/Toggle";
import SubJobCard from "@/components/jobs/SubJobCard";
import { updateJob, deleteJob } from "@/lib/api";
import type { JobData, SubJobData, AccountsResponse } from "@/types";

interface JobCardProps {
  job: JobData;
  accounts: AccountsResponse;
  bannerOptions: string[];
  backgroundOptions: string[];
  onUpdated: (updated: JobData) => void;
  onDeleted: () => void;
}

const STATUS_OPTIONS = ["pending", "ready", "running", "done", "failed"];
const STATUS_COLORS: Record<string, string> = {
  pending:  "bg-yellow-900/40 text-yellow-300",
  ready:    "bg-emerald-900/40 text-emerald-300",
  running:  "bg-blue-900/40 text-blue-300",
  done:     "bg-green-900/40 text-green-300",
  failed:   "bg-red-900/40 text-red-300",
};

const LOCKED_STATUSES = new Set(["ready", "running", "done", "failed"]);

export default function JobCard({ job, accounts, bannerOptions, backgroundOptions, onUpdated, onDeleted }: JobCardProps) {
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [starting, setStarting] = useState(false);

  const [youtubeUrl, setYoutubeUrl] = useState(job.youtube_url);
  const [trace, setTrace] = useState(job.trace ?? "");
  const [isVertical, setIsVertical] = useState(job.is_vertical);
  const [isSubtitles, setIsSubtitles] = useState(job.is_subtitles);
  const [isSlice, setIsSlice] = useState(job.is_slice);
  const [isBackground, setIsBackground] = useState(job.is_background);
  const [isCircle, setIsCircle] = useState(job.is_circle);
  const [minVideoDuration, setMinVideoDuration] = useState(job.min_video_duration ?? 60);
  const [backgroundName, setBackgroundName] = useState(job.background_name ?? "");

  const locked = LOCKED_STATUSES.has(job.status);

  function resetEdit() {
    setYoutubeUrl(job.youtube_url);
    setTrace(job.trace ?? "");
    setIsVertical(job.is_vertical);
    setIsSubtitles(job.is_subtitles);
    setIsSlice(job.is_slice);
    setIsBackground(job.is_background);
    setIsCircle(job.is_circle);
    setBackgroundName(job.background_name ?? "");
    setMinVideoDuration(job.min_video_duration ?? 60);
    setEditing(false);
  }

  async function handleSave() {
    setSaving(true);
    try {
      const updated = await updateJob(job.job_id, {
        youtube_url: youtubeUrl,
        trace: trace || null,
        is_vertical: isVertical,
        is_subtitles: isSubtitles,
        is_slice: isSlice,
        is_background: isBackground,
        is_circle: isCircle,
        min_video_duration: minVideoDuration,
      });
      onUpdated({ ...updated, sub_jobs: job.sub_jobs });
      setEditing(false);
    } finally {
      setSaving(false);
    }
  }

  async function handleStartJob() {
    if (!confirm(`Start job ${job.job_id}? It will be locked for editing.`)) return;
    setStarting(true);
    try {
      const updated = await updateJob(job.job_id, { status: "ready" });
      onUpdated({ ...updated, sub_jobs: job.sub_jobs });
    } finally {
      setStarting(false);
    }
  }

  async function handleDelete() {
    if (!confirm(`Delete job ${job.job_id}? This will also delete all sub-jobs.`)) return;
    await deleteJob(job.job_id);
    onDeleted();
  }

  function handleSubJobUpdated(updated: SubJobData) {
    onUpdated({ ...job, sub_jobs: job.sub_jobs.map((s) => s.sub_job_id === updated.sub_job_id ? updated : s) });
  }

  function handleSubJobDeleted(subJobId: string) {
    onUpdated({ ...job, sub_jobs: job.sub_jobs.filter((s) => s.sub_job_id !== subJobId) });
  }

  return (
    <div className="bg-slate-800 border border-slate-700 rounded-xl overflow-hidden">
      {/* Header */}
      <div className="flex items-center gap-3 px-5 py-3">
        <button onClick={() => setOpen((o) => !o)} className="flex items-center gap-3 flex-1 min-w-0 text-left">
          <svg className={`w-4 h-4 shrink-0 text-slate-500 transition-transform ${open ? "rotate-90" : ""}`} fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
          </svg>
          <span className="text-sm font-mono text-slate-300 shrink-0">{job.job_id}</span>
          <span className={`text-xs font-medium px-2 py-0.5 rounded shrink-0 ${STATUS_COLORS[job.status] ?? "bg-slate-700 text-slate-300"}`}>
            {job.status}
          </span>
          <span className="text-sm text-slate-400 truncate">{job.youtube_url}</span>
          <span className="text-xs text-slate-500 ml-auto shrink-0">{job.sub_jobs.length} sub</span>
        </button>
        {!locked && (
          <button
            onClick={handleStartJob}
            disabled={starting}
            className="text-xs font-medium bg-emerald-700 hover:bg-emerald-600 disabled:opacity-40 text-white px-3 py-1 rounded-md transition-colors shrink-0"
          >
            {starting ? "…" : "Start"}
          </button>
        )}
        {!locked && (
          <button onClick={handleDelete} className="text-slate-500 hover:text-red-400 transition-colors text-xl leading-none px-1">×</button>
        )}
      </div>

      {/* Body */}
      {open && (
        <div className="border-t border-slate-700 px-5 py-4 flex flex-col gap-4">
          {!editing ? (
            <>
              <div className="grid grid-cols-2 gap-x-8 gap-y-1 text-sm">
                <Row label="YouTube URL" value={job.youtube_url || "—"} />
                <Row label="Status" value={job.status} />
                <Row label="Trace" value={job.trace ?? "—"} />
                <Row label="Background" value={job.background_name ?? "—"} />
                <Row label="Min clip duration" value={`${job.min_video_duration ?? 60}s`} />
                <Row label="Created" value={job.create_datetime ? new Date(job.create_datetime).toLocaleString() : "—"} />
                <Row label="Updated" value={job.last_change_datetime ? new Date(job.last_change_datetime).toLocaleString() : "—"} />
              </div>
              <div className="flex flex-wrap gap-3 text-sm">
                {([
                  ["Vertical", job.is_vertical],
                  ["Subtitles", job.is_subtitles],
                  ["Slice", job.is_slice],
                  ["Background", job.is_background],
                  ["Circle", job.is_circle],
                ] as [string, boolean][]).map(([label, val]) => (
                  <span key={label} className={`px-2 py-0.5 rounded text-xs ${val ? "bg-indigo-900/50 text-indigo-300" : "bg-slate-700 text-slate-500"}`}>
                    {label}
                  </span>
                ))}
              </div>
              {!locked && (
                <button onClick={() => setEditing(true)} className="self-start text-xs text-indigo-400 hover:text-indigo-300 transition-colors">
                  Edit
                </button>
              )}
            </>
          ) : (
            <div className="flex flex-col gap-3">
              <div className="flex flex-col gap-1">
                <label className="text-xs text-slate-400">YouTube URL</label>
                <input type="text" value={youtubeUrl} onChange={(e) => setYoutubeUrl(e.target.value)}
                  className="bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500" />
              </div>
              <div className="flex flex-col gap-1">
                <label className="text-xs text-slate-400">Trace</label>
                <input type="text" value={trace} onChange={(e) => setTrace(e.target.value)}
                  className="bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500" />
              </div>
              <div className="flex flex-col gap-1">
                <label className="text-xs text-slate-400">Min clip duration (sec)</label>
                <input type="number" min={1} value={minVideoDuration} onChange={(e) => setMinVideoDuration(Number(e.target.value))}
                  className="bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500 w-32" />
              </div>
              <div className="flex flex-col gap-2">
                <Toggle label="Make vertical?" checked={isVertical} onChange={setIsVertical} />
                <Toggle label="Add subtitles?" checked={isSubtitles} onChange={setIsSubtitles} />
                <Toggle label="Slice video?" checked={isSlice} onChange={setIsSlice} />
                <Toggle label="Add background?" checked={isBackground} onChange={setIsBackground} />
                <Toggle label="Circle?" checked={isCircle} onChange={setIsCircle} />
              </div>
              {isBackground && (
                <div className="flex flex-col gap-1">
                  <label className="text-xs text-slate-400">Background</label>
                  <select value={backgroundName} onChange={(e) => setBackgroundName(e.target.value)}
                    className="bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500">
                    <option value="">— none —</option>
                    {backgroundOptions.map((b) => <option key={b} value={b}>{b}</option>)}
                  </select>
                </div>
              )}
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

          {/* Sub-jobs */}
          {job.sub_jobs.length > 0 && (
            <div className="flex flex-col gap-2 mt-1">
              <p className="text-xs font-semibold uppercase tracking-widest text-slate-500">Sub-jobs</p>
              {job.sub_jobs.map((sj) => (
                <SubJobCard
                  key={sj.sub_job_id}
                  subJob={sj}
                  accounts={accounts}
                  bannerOptions={bannerOptions}
                  locked={locked}
                  onUpdated={handleSubJobUpdated}
                  onDeleted={() => handleSubJobDeleted(sj.sub_job_id)}
                />
              ))}
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
      <span className="text-slate-200 break-all">{value}</span>
    </>
  );
}
