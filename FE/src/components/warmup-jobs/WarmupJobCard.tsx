"use client";

import { useState } from "react";
import { updateWarmupJob, deleteWarmupJob } from "@/lib/api";
import { encodeMaxVideos, decodeMaxVideos } from "@/components/warmup-jobs/WarmupJobFormCard";
import type { MaxVideosMode } from "@/components/warmup-jobs/WarmupJobFormCard";
import type { WarmupJob, AccountRecord } from "@/types";

interface Props {
  job: WarmupJob;
  accounts: AccountRecord[];
  selected: boolean;
  onToggleSelect: () => void;
  onUpdated: (updated: WarmupJob) => void;
  onDeleted: () => void;
}

const TYPE_OPTIONS = ["one-time", "cron"];

const STATUS_COLORS: Record<string, string> = {
  pending:    "bg-yellow-900/40 text-yellow-300",
  warming_up: "bg-blue-900/40 text-blue-300",
  done:       "bg-green-900/40 text-green-300",
};

const REPEAT_UNITS = [
  { label: "sec",  factor: 1 },
  { label: "min",  factor: 60 },
  { label: "hour", factor: 3600 },
  { label: "day",  factor: 86400 },
];

function bestUnit(seconds: number): { value: number; unitIdx: number } {
  for (let i = REPEAT_UNITS.length - 1; i >= 0; i--) {
    if (seconds % REPEAT_UNITS[i].factor === 0) {
      return { value: seconds / REPEAT_UNITS[i].factor, unitIdx: i };
    }
  }
  return { value: seconds, unitIdx: 0 };
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <>
      <span className="text-slate-500">{label}</span>
      <span className="text-slate-200">{value}</span>
    </>
  );
}

export default function WarmupJobCard({ job, accounts, selected, onToggleSelect, onUpdated, onDeleted }: Props) {
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);

  // edit state
  const [accountId, setAccountId] = useState(job.account_id);
  const [type, setType] = useState(job.type);
  const [warmUpDatetime, setWarmUpDatetime] = useState(
    job.warm_up_datetime ? job.warm_up_datetime.slice(0, 16) : ""
  );
  const initUnit = job.repeat_step ? bestUnit(job.repeat_step) : { value: 1, unitIdx: 0 };
  const [repeatValue, setRepeatValue] = useState(initUnit.value);
  const [repeatUnitIdx, setRepeatUnitIdx] = useState(initUnit.unitIdx);

  const initMv = decodeMaxVideos(job.max_videos ?? "50,100");
  const [maxVideosMode, setMaxVideosMode] = useState<MaxVideosMode>(initMv.mode);
  const [maxVideosX, setMaxVideosX] = useState(initMv.x);
  const [maxVideosY, setMaxVideosY] = useState(initMv.y);

  const canEdit = job.status === "pending";

  function resetEdit() {
    setAccountId(job.account_id);
    setType(job.type);
    setWarmUpDatetime(job.warm_up_datetime ? job.warm_up_datetime.slice(0, 16) : "");
    const u = job.repeat_step ? bestUnit(job.repeat_step) : { value: 1, unitIdx: 0 };
    setRepeatValue(u.value);
    setRepeatUnitIdx(u.unitIdx);
    const mv = decodeMaxVideos(job.max_videos ?? "50,100");
    setMaxVideosMode(mv.mode);
    setMaxVideosX(mv.x);
    setMaxVideosY(mv.y);
    setEditing(false);
  }

  async function handleSave() {
    setSaving(true);
    try {
      const repeatStep = type === "cron" ? repeatValue * REPEAT_UNITS[repeatUnitIdx].factor : null;
      const updated = await updateWarmupJob(job.job_id, {
        account_id: accountId,
        type,
        warm_up_datetime: warmUpDatetime ? new Date(warmUpDatetime).toISOString() : null,
        repeat_step: repeatStep,
        max_videos: encodeMaxVideos(maxVideosMode, maxVideosX, maxVideosY),
      });
      onUpdated(updated);
      setEditing(false);
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!confirm(`Delete warmup job ${job.job_id}?`)) return;
    await deleteWarmupJob(job.job_id);
    onDeleted();
  }

  const repeatDisplay = job.repeat_step
    ? (() => { const u = bestUnit(job.repeat_step); return `${u.value} ${REPEAT_UNITS[u.unitIdx].label}`; })()
    : "—";

  return (
    <div className="bg-slate-800 border border-slate-700 rounded-xl overflow-hidden">
      {/* Header */}
      <div className="flex items-center gap-3 px-5 py-3">
        <input
          type="checkbox"
          checked={selected}
          onChange={onToggleSelect}
          onClick={(e) => e.stopPropagation()}
          className="w-4 h-4 shrink-0 rounded accent-indigo-500 cursor-pointer"
        />
        <button onClick={() => setOpen((o) => !o)} className="flex items-center gap-3 flex-1 min-w-0 text-left">
          <svg className={`w-4 h-4 shrink-0 text-slate-500 transition-transform ${open ? "rotate-90" : ""}`} fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
          </svg>
          <span className="text-sm font-mono text-slate-300 shrink-0">{job.job_id}</span>
          <span className={`text-xs font-medium px-2 py-0.5 rounded shrink-0 ${STATUS_COLORS[job.status] ?? "bg-slate-700 text-slate-300"}`}>
            {job.status}
          </span>
          {job.is_archived && (
            <span className="text-xs font-medium px-2 py-0.5 rounded shrink-0 bg-slate-700 text-slate-400">archived</span>
          )}
          <span className="text-sm text-slate-400 truncate">
            {job.account_platform && <span className="text-slate-500 mr-1">[{job.account_platform}]</span>}
            {job.account_name ?? `account #${job.account_id}`}
          </span>
          <span className="text-xs text-slate-500 ml-auto shrink-0">{job.type}</span>
        </button>

        {canEdit && (
          <button onClick={handleDelete} className="text-slate-500 hover:text-red-400 transition-colors text-xl leading-none px-1">×</button>
        )}
      </div>

      {/* Body */}
      {open && (
        <div className="border-t border-slate-700 px-5 py-4 flex flex-col gap-4">
          {!editing ? (
            <>
              <div className="grid grid-cols-2 gap-x-8 gap-y-1 text-sm">
                <Row label="Account" value={`[${job.account_platform ?? "?"}] ${job.account_name ?? job.account_id}`} />
                <Row label="Status" value={job.status} />
                <Row label="Type" value={job.type} />
                <Row label="Datetime" value={job.warm_up_datetime ? new Date(job.warm_up_datetime).toLocaleString() : "—"} />
                {job.type === "cron" && <Row label="Repeat every" value={repeatDisplay} />}
                <Row label="Max shorts" value={(() => {
                  const mv = decodeMaxVideos(job.max_videos ?? "50,100");
                  return mv.mode === "fixed" ? String(mv.x) : `rand(${mv.x}, ${mv.y})`;
                })()} />
              </div>

              {canEdit && (
                <div className="flex gap-2">
                  <button
                    onClick={() => setEditing(true)}
                    className="text-xs font-medium bg-slate-700 hover:bg-slate-600 text-white px-3 py-1 rounded-md transition-colors"
                  >
                    Edit
                  </button>
                </div>
              )}
            </>
          ) : (
            <div className="flex flex-col gap-3">
              {/* Account */}
              <div className="flex flex-col gap-1">
                <label className="text-xs text-slate-400">Account</label>
                <select
                  value={accountId}
                  onChange={(e) => setAccountId(Number(e.target.value))}
                  className="bg-slate-700 border border-slate-600 text-slate-100 text-sm rounded-md px-3 py-1.5 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                >
                  {accounts.map((a) => (
                    <option key={a.id} value={a.id}>[{a.platform}] {a.name}</option>
                  ))}
                </select>
              </div>

              {/* Type */}
              <div className="flex flex-col gap-1">
                <label className="text-xs text-slate-400">Type</label>
                <select
                  value={type}
                  onChange={(e) => setType(e.target.value)}
                  className="bg-slate-700 border border-slate-600 text-slate-100 text-sm rounded-md px-3 py-1.5 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                >
                  {TYPE_OPTIONS.map((t) => <option key={t} value={t}>{t}</option>)}
                </select>
              </div>

              {/* Datetime */}
              <div className="flex flex-col gap-1">
                <label className="text-xs text-slate-400">Warm-up datetime</label>
                <input
                  type="datetime-local"
                  value={warmUpDatetime}
                  onChange={(e) => setWarmUpDatetime(e.target.value)}
                  className="bg-slate-700 border border-slate-600 text-slate-100 text-sm rounded-md px-3 py-1.5 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                />
              </div>

              {/* Repeat step (cron only) */}
              {type === "cron" && (
                <div className="flex flex-col gap-1">
                  <label className="text-xs text-slate-400">Repeat every</label>
                  <div className="flex gap-2">
                    <input
                      type="number"
                      min={1}
                      value={repeatValue}
                      onChange={(e) => setRepeatValue(Math.max(1, Number(e.target.value)))}
                      className="w-24 bg-slate-700 border border-slate-600 text-slate-100 text-sm rounded-md px-3 py-1.5 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                    />
                    <select
                      value={repeatUnitIdx}
                      onChange={(e) => setRepeatUnitIdx(Number(e.target.value))}
                      className="bg-slate-700 border border-slate-600 text-slate-100 text-sm rounded-md px-3 py-1.5 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                    >
                      {REPEAT_UNITS.map((u, i) => <option key={u.label} value={i}>{u.label}</option>)}
                    </select>
                  </div>
                </div>
              )}

              {/* Max videos */}
              <div className="flex flex-col gap-1">
                <label className="text-xs text-slate-400">Max shorts per job</label>
                <div className="flex gap-2 mb-1">
                  {(["fixed", "rand"] as MaxVideosMode[]).map((m) => (
                    <button
                      key={m}
                      type="button"
                      onClick={() => setMaxVideosMode(m)}
                      className={`px-3 py-1 text-xs rounded-md border transition-colors ${
                        maxVideosMode === m
                          ? "bg-indigo-600 border-indigo-500 text-white"
                          : "bg-slate-700 border-slate-600 text-slate-300 hover:bg-slate-600"
                      }`}
                    >
                      {m === "fixed" ? "Fixed" : "Random"}
                    </button>
                  ))}
                </div>
                {maxVideosMode === "fixed" ? (
                  <input
                    type="number"
                    min={1}
                    value={maxVideosX}
                    onChange={(e) => setMaxVideosX(Math.max(1, Number(e.target.value)))}
                    className="w-24 bg-slate-700 border border-slate-600 text-slate-100 text-sm rounded-md px-3 py-1.5 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                  />
                ) : (
                  <div className="flex items-center gap-2">
                    <input
                      type="number"
                      min={1}
                      value={maxVideosX}
                      onChange={(e) => setMaxVideosX(Math.max(1, Number(e.target.value)))}
                      className="w-20 bg-slate-700 border border-slate-600 text-slate-100 text-sm rounded-md px-3 py-1.5 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                    />
                    <span className="text-xs text-slate-500">–</span>
                    <input
                      type="number"
                      min={maxVideosX + 1}
                      value={maxVideosY}
                      onChange={(e) => setMaxVideosY(Math.max(maxVideosX + 1, Number(e.target.value)))}
                      className="w-20 bg-slate-700 border border-slate-600 text-slate-100 text-sm rounded-md px-3 py-1.5 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                    />
                  </div>
                )}
              </div>

              <div className="flex gap-2 pt-1">
                <button
                  onClick={handleSave}
                  disabled={saving}
                  className="text-xs font-medium bg-indigo-600 hover:bg-indigo-500 disabled:opacity-40 text-white px-4 py-1.5 rounded-md transition-colors"
                >
                  {saving ? "Saving…" : "Save"}
                </button>
                <button
                  onClick={resetEdit}
                  className="text-xs font-medium bg-slate-700 hover:bg-slate-600 text-white px-4 py-1.5 rounded-md transition-colors"
                >
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
