import type { AccountsResponse, VideoCardData, JobData, SubJobData, AccountRecord, AssetRecord, WarmupJob } from "@/types";

const BASE_URL = "/api";

export async function fetchBackgrounds(): Promise<string[]> {
  const res = await fetch(`${BASE_URL}/backgrounds`);
  if (!res.ok) throw new Error("Failed to fetch backgrounds");
  return res.json();
}

export async function fetchBanners(): Promise<string[]> {
  const res = await fetch(`${BASE_URL}/banners`);
  if (!res.ok) throw new Error("Failed to fetch banners");
  return res.json();
}

export async function fetchAccounts(): Promise<AccountsResponse> {
  const res = await fetch(`${BASE_URL}/accounts`);
  if (!res.ok) throw new Error("Failed to fetch accounts");
  return res.json();
}

export async function fetchJobs(showArchived = false): Promise<JobData[]> {
  const res = await fetch(`${BASE_URL}/jobs?show_archived=${showArchived}`);
  if (!res.ok) throw new Error("Failed to fetch jobs");
  return res.json();
}

export async function updateJob(jobId: string, data: Partial<JobData>): Promise<JobData> {
  const res = await fetch(`${BASE_URL}/jobs/${jobId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
  if (!res.ok) throw new Error("Failed to update job");
  return res.json();
}

export async function deleteJob(jobId: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/jobs/${jobId}`, { method: "DELETE" });
  if (!res.ok) throw new Error("Failed to delete job");
}

export async function updateSubJob(subJobId: string, data: {
  publish_datetime?: string | null;
  account_names?: string[];
  banner_name?: string | null;
}): Promise<SubJobData> {
  const res = await fetch(`${BASE_URL}/sub_jobs/${subJobId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
  if (!res.ok) throw new Error("Failed to update sub_job");
  return res.json();
}

export async function deleteSubJob(subJobId: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/sub_jobs/${subJobId}`, { method: "DELETE" });
  if (!res.ok) throw new Error("Failed to delete sub_job");
}

// ── Accounts ─────────────────────────────────────────────────────────────────
export async function fetchAccountsList(): Promise<AccountRecord[]> {
  const res = await fetch(`${BASE_URL}/accounts/list`);
  if (!res.ok) throw new Error("Failed to fetch accounts list");
  return res.json();
}
export async function updateAccount(id: number, data: Partial<AccountRecord>): Promise<AccountRecord> {
  const res = await fetch(`${BASE_URL}/accounts/${id}`, {
    method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify(data),
  });
  if (!res.ok) throw new Error("Failed to update account");
  return res.json();
}
export async function deleteAccount(id: number): Promise<void> {
  await fetch(`${BASE_URL}/accounts/${id}`, { method: "DELETE" });
}

// ── Banners ───────────────────────────────────────────────────────────────────
export async function fetchBannersList(): Promise<AssetRecord[]> {
  const res = await fetch(`${BASE_URL}/banners/list`);
  if (!res.ok) throw new Error("Failed to fetch banners list");
  return res.json();
}
export async function updateBanner(id: number, data: { name?: string; note?: string }): Promise<AssetRecord> {
  const res = await fetch(`${BASE_URL}/banners/${id}`, {
    method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify(data),
  });
  if (!res.ok) throw new Error("Failed to update banner");
  return res.json();
}
export async function deleteBanner(id: number): Promise<void> {
  await fetch(`${BASE_URL}/banners/${id}`, { method: "DELETE" });
}

// ── Backgrounds ───────────────────────────────────────────────────────────────
export async function fetchBackgroundsList(): Promise<AssetRecord[]> {
  const res = await fetch(`${BASE_URL}/backgrounds/list`);
  if (!res.ok) throw new Error("Failed to fetch backgrounds list");
  return res.json();
}
export async function updateBackground(id: number, data: { name?: string; note?: string }): Promise<AssetRecord> {
  const res = await fetch(`${BASE_URL}/backgrounds/${id}`, {
    method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify(data),
  });
  if (!res.ok) throw new Error("Failed to update background");
  return res.json();
}
export async function deleteBackground(id: number): Promise<void> {
  await fetch(`${BASE_URL}/backgrounds/${id}`, { method: "DELETE" });
}

// ── Auth Capture ──────────────────────────────────────────────────────────────
export async function captureAuthStart(platform: string, name: string): Promise<{ status: string }> {
  const res = await fetch(`${BASE_URL}/accounts/capture-auth/start?platform=${encodeURIComponent(platform)}&name=${encodeURIComponent(name)}`, { method: "POST" });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

export async function captureAuthFinish(accountId?: number): Promise<{ file_name: string; platform: string; name: string }> {
  const url = accountId
    ? `${BASE_URL}/accounts/capture-auth/finish?account_id=${accountId}`
    : `${BASE_URL}/accounts/capture-auth/finish`;
  const res = await fetch(url, { method: "POST" });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

export async function captureAuthStatus(): Promise<{ active: boolean; platform: string | null; name: string | null }> {
  const res = await fetch(`${BASE_URL}/accounts/capture-auth/status`);
  if (!res.ok) return { active: false, platform: null, name: null };
  return res.json();
}

// ── Warmup Jobs ───────────────────────────────────────────────────────────────
export async function fetchWarmupJobs(showArchived = false): Promise<WarmupJob[]> {
  const res = await fetch(`${BASE_URL}/warmup_jobs?show_archived=${showArchived}`);
  if (!res.ok) throw new Error("Failed to fetch warmup jobs");
  return res.json();
}

export async function createWarmupJob(data: {
  account_id: number;
  type: string;
  warm_up_datetime?: string | null;
  repeat_step?: number | null;
  max_videos?: string;
}): Promise<WarmupJob> {
  const res = await fetch(`${BASE_URL}/warmup_jobs`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
  if (!res.ok) throw new Error("Failed to create warmup job");
  return res.json();
}

export async function updateWarmupJob(jobId: string, data: Partial<{
  account_id: number;
  status: string;
  type: string;
  warm_up_datetime: string | null;
  repeat_step: number | null;
  max_videos: string;
}>): Promise<WarmupJob> {
  const res = await fetch(`${BASE_URL}/warmup_jobs/${jobId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
  if (!res.ok) throw new Error("Failed to update warmup job");
  return res.json();
}

export async function deleteWarmupJob(jobId: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/warmup_jobs/${jobId}`, { method: "DELETE" });
  if (!res.ok) throw new Error("Failed to delete warmup job");
}

export async function submitJobs(cards: VideoCardData[]): Promise<{ status: string; job_id: string }[]> {
  const results = await Promise.all(
    cards.map((card) =>
      fetch(`${BASE_URL}/jobs`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(card),
      }).then((r) => r.json())
    )
  );
  return results;
}
