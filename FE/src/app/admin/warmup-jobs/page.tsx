"use client";

import { useEffect, useState, useCallback } from "react";
import WarmupJobCard from "@/components/warmup-jobs/WarmupJobCard";
import { fetchWarmupJobs, fetchAccountsList, updateWarmupJob } from "@/lib/api";
import type { WarmupJob, AccountRecord } from "@/types";

export default function WarmupJobsPage() {
  const [jobs, setJobs] = useState<WarmupJob[]>([]);
  const [accounts, setAccounts] = useState<AccountRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [showArchived, setShowArchived] = useState(false);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [archiving, setArchiving] = useState(false);

  const load = useCallback(async (archived = false) => {
    setLoading(true);
    setSelectedIds(new Set());
    try {
      const [j, a] = await Promise.all([fetchWarmupJobs(archived), fetchAccountsList()]);
      setJobs(j);
      setAccounts(a);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(showArchived); }, [load, showArchived]);

  function handleUpdated(updated: WarmupJob) {
    setJobs((prev) => prev.map((j) => j.job_id === updated.job_id ? updated : j));
  }

  function handleDeleted(jobId: string) {
    setJobs((prev) => prev.filter((j) => j.job_id !== jobId));
    setSelectedIds((prev) => { const s = new Set(prev); s.delete(jobId); return s; });
  }

  function toggleSelect(jobId: string) {
    setSelectedIds((prev) => {
      const s = new Set(prev);
      s.has(jobId) ? s.delete(jobId) : s.add(jobId);
      return s;
    });
  }

  const archivableIds = jobs.filter((j) => j.status === "done" && !j.is_archived).map((j) => j.job_id);
  const allSelected = archivableIds.length > 0 && archivableIds.every((id) => selectedIds.has(id));

  function toggleSelectAll() {
    if (allSelected) {
      setSelectedIds((prev) => {
        const s = new Set(prev);
        archivableIds.forEach((id) => s.delete(id));
        return s;
      });
    } else {
      setSelectedIds((prev) => new Set([...prev, ...archivableIds]));
    }
  }

  async function handleArchiveSelected() {
    const ids = [...selectedIds].filter((id) => archivableIds.includes(id));
    if (ids.length === 0) return;
    setArchiving(true);
    try {
      await Promise.all(ids.map((id) => updateWarmupJob(id, { is_archived: true })));
      setJobs((prev) => prev.map((j) => ids.includes(j.job_id) ? { ...j, is_archived: true } : j));
      setSelectedIds(new Set());
      if (!showArchived) {
        setJobs((prev) => prev.filter((j) => !ids.includes(j.job_id)));
      }
    } finally {
      setArchiving(false);
    }
  }

  const selectedArchivableCount = [...selectedIds].filter((id) => archivableIds.includes(id)).length;

  return (
    <div className="p-6 flex flex-col gap-6 min-h-full">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-slate-100">Warmup Jobs</h1>
        <div className="flex items-center gap-3">
          <label className="flex items-center gap-2 text-sm text-slate-400 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={showArchived}
              onChange={(e) => setShowArchived(e.target.checked)}
              className="w-4 h-4 rounded accent-indigo-500"
            />
            Show archived
          </label>
          {selectedArchivableCount > 0 && (
            <button
              onClick={handleArchiveSelected}
              disabled={archiving}
              className="flex items-center gap-2 bg-slate-700 hover:bg-slate-600 disabled:opacity-40 text-white text-sm font-medium px-4 py-2 rounded-lg border border-slate-600 transition-colors"
            >
              {archiving ? "Archiving…" : `Archive (${selectedArchivableCount})`}
            </button>
          )}
          <button
            onClick={() => load(showArchived)}
            disabled={loading}
            className="flex items-center gap-2 bg-slate-700 hover:bg-slate-600 disabled:opacity-40 text-white text-sm font-medium px-4 py-2 rounded-lg border border-slate-600 transition-colors"
          >
            {loading ? "Loading…" : "Refresh"}
          </button>
        </div>
      </div>

      {!loading && jobs.length === 0 && (
        <div className="flex flex-1 items-center justify-center">
          <p className="text-slate-500 text-sm">No warmup jobs yet.</p>
        </div>
      )}

      {jobs.length > 0 && archivableIds.length > 0 && (
        <label className="flex items-center gap-2 text-sm text-slate-400 cursor-pointer select-none w-fit">
          <input
            type="checkbox"
            checked={allSelected}
            onChange={toggleSelectAll}
            className="w-4 h-4 rounded accent-indigo-500"
          />
          Select all archivable
        </label>
      )}

      <div className="flex flex-col gap-3">
        {jobs.map((job) => (
          <WarmupJobCard
            key={job.job_id}
            job={job}
            accounts={accounts}
            selected={selectedIds.has(job.job_id)}
            onToggleSelect={() => toggleSelect(job.job_id)}
            onUpdated={handleUpdated}
            onDeleted={() => handleDeleted(job.job_id)}
          />
        ))}
      </div>
    </div>
  );
}
