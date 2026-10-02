import type { AccountsResponse, VideoCardData, JobData, SubJobData, AccountRecord, AssetRecord, OutData, Channel, Clip } from "@/types";

/** Convert UTC ISO string to UTC+7 formatted string (DD.MM.YYYY HH:MM) */
export function toUTC7(iso: string): string {
  const date = new Date(iso);
  const shifted = new Date(date.getTime() + 7 * 60 * 60 * 1000);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(shifted.getUTCDate())}.${pad(shifted.getUTCMonth() + 1)}.${shifted.getUTCFullYear()} ${pad(shifted.getUTCHours())}:${pad(shifted.getUTCMinutes())}`;
}

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
export async function fetchAccountClips(id: number): Promise<OutData[]> {
  const res = await fetch(`${BASE_URL}/accounts/${id}/clips`);
  if (!res.ok) throw new Error("Failed to fetch clips");
  return res.json();
}
export async function fetchAccountScheduled(id: number): Promise<OutData[]> {
  const res = await fetch(`${BASE_URL}/accounts/${id}/scheduled`);
  if (!res.ok) throw new Error("Failed to fetch account schedule");
  return res.json();
}
export async function fetchAccountPublished(id: number): Promise<OutData[]> {
  const res = await fetch(`${BASE_URL}/accounts/${id}/published`);
  if (!res.ok) throw new Error("Failed to fetch published clips");
  return res.json();
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

// ── Asset upload (browser -> MinIO via presigned URL) ────────────────────────
export async function uploadAsset(kind: "banners" | "backgrounds", name: string, note: string, file: File): Promise<AssetRecord> {
  const pre = await fetch(`${BASE_URL}/${kind}/presign`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ file_name: file.name }),
  });
  if (!pre.ok) throw new Error("Failed to get upload URL");
  const { url, bucket, object } = await pre.json();

  const put = await fetch(url, { method: "PUT", body: file });
  if (!put.ok) throw new Error("Failed to upload file");

  const reg = await fetch(`${BASE_URL}/${kind}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, note: note || null, bucket, object }),
  });
  if (!reg.ok) throw new Error("Failed to register file");
  return reg.json();
}

// ── Channels (monitoring) ────────────────────────────────────────────────────
export async function fetchChannels(): Promise<Channel[]> {
  const res = await fetch(`${BASE_URL}/channels`);
  if (!res.ok) throw new Error("Failed to fetch channels");
  return res.json();
}

export async function createChannel(data: { name: string; url: string; approve_mode: string; spec: Partial<VideoCardData> }): Promise<Channel> {
  const res = await fetch(`${BASE_URL}/channels`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
  if (!res.ok) throw new Error((await res.json().catch(() => ({}))).detail ?? "Failed to create channel");
  return res.json();
}

export async function updateChannel(id: number, data: Partial<Pick<Channel, "name" | "approve_mode" | "enabled">>): Promise<Channel> {
  const res = await fetch(`${BASE_URL}/channels/${id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
  if (!res.ok) throw new Error("Failed to update channel");
  return res.json();
}

export async function deleteChannel(id: number): Promise<void> {
  const res = await fetch(`${BASE_URL}/channels/${id}`, { method: "DELETE" });
  if (!res.ok) throw new Error("Failed to delete channel");
}

// ── Clips / approval ─────────────────────────────────────────────────────────
export async function fetchClips(approval = ""): Promise<Clip[]> {
  const res = await fetch(`${BASE_URL}/clips?approval=${approval}`);
  if (!res.ok) throw new Error("Failed to fetch clips");
  return res.json();
}

export async function approveClip(id: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/clips/${id}/approve`, { method: "POST" });
  if (!res.ok) throw new Error("Failed to approve clip");
}

export async function rejectClip(id: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/clips/${id}/reject`, { method: "POST" });
  if (!res.ok) throw new Error("Failed to reject clip");
}
