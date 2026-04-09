"use client";

import { useEffect, useState, useCallback } from "react";
import WarmupJobCard from "@/components/warmup-jobs/WarmupJobCard";
import { fetchWarmupJobs, fetchAccountsList } from "@/lib/api";
import type { WarmupJob, AccountRecord } from "@/types";

export default function WarmupJobsPage() {
  const [jobs, setJobs] = useState<WarmupJob[]>([]);
  const [accounts, setAccounts] = useState<AccountRecord[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [j, a] = await Promise.all([fetchWarmupJobs(), fetchAccountsList()]);
      setJobs(j);
      setAccounts(a);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  function handleUpdated(updated: WarmupJob) {
    setJobs((prev) => prev.map((j) => j.job_id === updated.job_id ? updated : j));
  }

  function handleDeleted(jobId: string) {
    setJobs((prev) => prev.filter((j) => j.job_id !== jobId));
  }

  return (
    <div className="p-6 flex flex-col gap-6 min-h-full">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-slate-100">Warmup Jobs</h1>
        <button
          onClick={load}
          disabled={loading}
          className="flex items-center gap-2 bg-slate-700 hover:bg-slate-600 disabled:opacity-40 text-white text-sm font-medium px-4 py-2 rounded-lg border border-slate-600 transition-colors"
        >
          {loading ? "Loading…" : "Refresh"}
        </button>
      </div>

      {!loading && jobs.length === 0 && (
        <div className="flex flex-1 items-center justify-center">
          <p className="text-slate-500 text-sm">No warmup jobs yet.</p>
        </div>
      )}

      <div className="flex flex-col gap-3">
        {jobs.map((job) => (
          <WarmupJobCard
            key={job.job_id}
            job={job}
            accounts={accounts}
            onUpdated={handleUpdated}
            onDeleted={() => handleDeleted(job.job_id)}
          />
        ))}
      </div>
    </div>
  );
}
