"use client";

import { useEffect, useState, useCallback } from "react";
import JobCard from "@/components/jobs/JobCard";
import { fetchJobs, fetchAccounts, fetchBanners, fetchBackgrounds } from "@/lib/api";
import type { JobData, AccountsResponse } from "@/types";

export default function JobsPage() {
  const [jobs, setJobs] = useState<JobData[]>([]);
  const [accounts, setAccounts] = useState<AccountsResponse>({ youtube: [], tiktok: [], instagram: [] });
  const [bannerOptions, setBannerOptions] = useState<string[]>([]);
  const [backgroundOptions, setBackgroundOptions] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [j, a, b, bg] = await Promise.all([fetchJobs(), fetchAccounts(), fetchBanners(), fetchBackgrounds()]);
      setJobs(j);
      setAccounts(a);
      setBannerOptions(b);
      setBackgroundOptions(bg);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  function handleUpdated(updated: JobData) {
    setJobs((prev) => prev.map((j) => j.job_id === updated.job_id ? updated : j));
  }

  function handleDeleted(jobId: string) {
    setJobs((prev) => prev.filter((j) => j.job_id !== jobId));
  }

  return (
    <div className="p-6 flex flex-col gap-6 min-h-full">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-slate-100">Jobs</h1>
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
          <p className="text-slate-500 text-sm">No jobs yet.</p>
        </div>
      )}

      <div className="flex flex-col gap-3">
        {jobs.map((job) => (
          <JobCard
            key={job.job_id}
            job={job}
            accounts={accounts}
            bannerOptions={bannerOptions}
            backgroundOptions={backgroundOptions}
            onUpdated={handleUpdated}
            onDeleted={() => handleDeleted(job.job_id)}
          />
        ))}
      </div>
    </div>
  );
}
