import type {
  AccountsResponse,
  VideoCardData,
  JobData,
  SubJobData,
  AccountRecord,
  AssetRecord,
  OutData,
  Channel,
  Clip,
  ClipEditor,
  PublicationEvent,
  PublicationHistory,
  PublicationTarget,
} from "@/types";
import { formatInZone } from "./datetime";
import type { AutoCompilationResult, Compilation, CompilationRequest, ImportedClip, TwitchSettings, TwitchChannel, TwitchClip, TwitchCollectorStatus } from "@/types/twitch";

export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
  ) {
    super(message);
    this.name = "ApiError";
  }
}
export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api${path}`, { ...init, cache: "no-store" });
  const body = await res.text();
  let data: unknown;
  try {
    data = body ? JSON.parse(body) : undefined;
  } catch {
    data = undefined;
  }
  if (!res.ok) {
    const error = data as { detail?: string; error?: string } | undefined;
    throw new ApiError(
      error?.detail ?? error?.error ?? `Request failed (${res.status})`,
      res.status,
    );
  }
  return data as T;
}
function json(method: string, data: unknown): RequestInit {
  return {
    method,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  };
}
export function toUTC7(iso: string) {
  return formatInZone(iso, "Asia/Tomsk");
}
export const fetchBackgrounds = () => request<string[]>("/backgrounds");
export const fetchBanners = () => request<string[]>("/banners");
export const fetchAccounts = () => request<AccountsResponse>("/accounts");
export const fetchJobs = (showArchived = false) =>
  request<JobData[]>(`/jobs?show_archived=${showArchived}`);
export const updateJob = (id: string, data: Partial<JobData>) =>
  request<JobData>(`/jobs/${encodeURIComponent(id)}`, json("PATCH", data));
export async function deleteJob(id: string) {
  await request(`/jobs/${encodeURIComponent(id)}`, { method: "DELETE" });
}
export const updateSubJob = (
  id: string,
  data: {
    publish_datetime?: string | null;
    account_names?: string[];
    banner_name?: string | null;
  },
) =>
  request<SubJobData>(
    `/sub_jobs/${encodeURIComponent(id)}`,
    json("PATCH", data),
  );
export async function deleteSubJob(id: string) {
  await request(`/sub_jobs/${encodeURIComponent(id)}`, { method: "DELETE" });
}
export const fetchAccountsList = () =>
  request<AccountRecord[]>("/accounts/list");
export const updateAccount = (id: number, data: Partial<AccountRecord>) =>
  request<AccountRecord>(`/accounts/${id}`, json("PATCH", data));
export async function deleteAccount(id: number) {
  await request(`/accounts/${id}`, { method: "DELETE" });
}
export const fetchAccountClips = (id: number) =>
  request<OutData[]>(`/accounts/${id}/clips`);
export const fetchAccountScheduled = (id: number) =>
  request<OutData[]>(`/accounts/${id}/scheduled`);
export const fetchAccountPublished = (id: number) =>
  request<OutData[]>(`/accounts/${id}/published`);
export const fetchBannersList = () => request<AssetRecord[]>("/banners/list");
export const updateBanner = (
  id: number,
  data: { name?: string; note?: string },
) => request<AssetRecord>(`/banners/${id}`, json("PATCH", data));
export async function deleteBanner(id: number) {
  await request(`/banners/${id}`, { method: "DELETE" });
}
export const fetchBackgroundsList = () =>
  request<AssetRecord[]>("/backgrounds/list");
export const updateBackground = (
  id: number,
  data: { name?: string; note?: string },
) => request<AssetRecord>(`/backgrounds/${id}`, json("PATCH", data));
export async function deleteBackground(id: number) {
  await request(`/backgrounds/${id}`, { method: "DELETE" });
}
export async function submitJobs(cards: VideoCardData[]) {
  return Promise.all(
    cards.map(async (card) => {
      try {
        const result = await request<{ status: string; job_id: string }>(
          "/jobs",
          json("POST", card),
        );
        return { id: card.id, ok: true as const, ...result };
      } catch (e) {
        return {
          id: card.id,
          ok: false as const,
          error: e instanceof Error ? e.message : "Failed to submit job",
        };
      }
    }),
  );
}
export async function uploadAsset(
  kind: "banners" | "backgrounds",
  name: string,
  note: string,
  file: File,
): Promise<AssetRecord> {
  const pre = await request<{ url: string; bucket: string; object: string }>(
    `/${kind}/presign`,
    json("POST", { file_name: file.name }),
  );
  const put = await fetch(pre.url, { method: "PUT", body: file });
  if (!put.ok)
    throw new ApiError(`File upload failed (${put.status})`, put.status);
  return request(
    `/${kind}`,
    json("POST", {
      name,
      note: note || null,
      bucket: pre.bucket,
      object: pre.object,
    }),
  );
}
export const fetchChannels = () => request<Channel[]>("/channels");
export const createChannel = (data: {
  name: string;
  url: string;
  approve_mode: string;
  spec: Partial<VideoCardData>;
}) => request<Channel>("/channels", json("POST", data));
export const updateChannel = (
  id: number,
  data: Partial<Pick<Channel, "name" | "approve_mode" | "enabled" | "spec">>,
) => request<Channel>(`/channels/${id}`, json("PATCH", data));
export async function deleteChannel(id: number) {
  await request(`/channels/${id}`, { method: "DELETE" });
}
export const fetchClips = (approval = "") =>
  request<Clip[]>(`/clips?approval=${encodeURIComponent(approval)}`);
export async function approveClip(id: string, version?: number) {
  await request(
    `/clips/${encodeURIComponent(id)}/approve`,
    json("POST", { expected_version: version }),
  );
}
export async function rejectClip(id: string) {
  await request(`/clips/${encodeURIComponent(id)}/reject`, { method: "POST" });
}
export async function fetchClipEditor(id: string): Promise<ClipEditor> {
  const data = await request<ClipEditor>(
    `/clips/${encodeURIComponent(id)}/editor`,
  );
  return {
    ...data,
    title: data.title ?? "",
    description: data.description ?? "",
    hashtags: data.hashtags ?? [],
    cues: data.cues ?? [],
  };
}
export const saveClipEditor = (id: string, data: ClipEditor) =>
  request<ClipEditor>(
    `/clips/${encodeURIComponent(id)}/editor`,
    json("PUT", {
      expected_version: data.version,
      start_sec: data.start_sec,
      end_sec: data.end_sec,
      title: data.title,
      description: data.description,
      visibility: data.visibility ?? "public",
      hashtags: data.hashtags,
      cues: data.cues,
      subtitles_enabled: data.subtitles_enabled,
    }),
  );
export const fetchPublications = (filters: Record<string, string>) =>
  request<PublicationEvent[]>(`/publications?${new URLSearchParams(filters)}`);
export const updatePublication = (
  id: number,
  data: { version: number; publish_at: string; timezone: string },
) =>
  request<PublicationEvent>(
    `/publications/${id}`,
    json("PATCH", { ...data, expected_version: data.version }),
  );
export const actPublication = (
  id: number,
  action: "approve" | "retry" | "cancel" | "publish-now",
  version: number,
) =>
  request<PublicationEvent>(
    `/publications/${id}/${action}`,
    json("POST", { expected_version: version }),
  );
export const fetchPublicationHistory = (id: number) =>
  request<PublicationHistory[]>(`/publications/${id}/history`);
export const connectAccount = (account: AccountRecord) =>
  request<{ url: string }>(
    `/accounts/${account.id}/oauth/connect`,
    json("POST", { expected_version: account.version }),
  );
export const checkAccount = (id: number) =>
  request<AccountRecord>(`/accounts/${id}/oauth/check`, { method: "POST" });
export const disconnectAccount = (account: AccountRecord) =>
  request<AccountRecord>(
    `/accounts/${account.id}/oauth/disconnect`,
    json("POST", { expected_version: account.version }),
  );
export const confirmAccount = (account: AccountRecord) =>
  request<AccountRecord>(
    `/accounts/${account.id}/oauth/confirm`,
    json("POST", {
      expected_version: account.version,
      remote_account_id: account.remote_account_id,
    }),
  );
export const retryClipRender = (id: string, version: number) =>
  request<ClipEditor>(
    `/clips/${encodeURIComponent(id)}/render`,
    json("POST", { expected_version: version }),
  );
export const restoreClipRevision = (
  id: string,
  version: number,
  revisionID: number,
) =>
  request<ClipEditor>(
    `/clips/${encodeURIComponent(id)}/restore`,
    json("POST", { expected_version: version, revision_id: revisionID }),
  );
export const fetchSubJobTargets = (id: string) =>
  request<{ version: number; targets: PublicationTarget[] }>(
    `/sub_jobs/${encodeURIComponent(id)}/targets`,
  );
export const saveSubJobTargets = (
  id: string,
  version: number,
  targets: PublicationTarget[],
) =>
  request<{ version: number; targets: PublicationTarget[] }>(
    `/sub_jobs/${encodeURIComponent(id)}/targets`,
    json("PUT", { expected_version: version, targets }),
  );

export const fetchTwitchSettings = () => request<TwitchSettings>("/twitch/settings");
export const saveTwitchSettings = (settings: TwitchSettings) => request<TwitchSettings>("/twitch/settings", json("PUT", settings));
export const fetchTwitchChannels = () => request<TwitchChannel[]>("/twitch/channels");
export const createTwitchChannel = (input: string) => request<TwitchChannel>("/twitch/channels", json("POST", { input }));
export const updateTwitchChannel = (id: string, paused: boolean) => request<TwitchChannel>(`/twitch/channels/${encodeURIComponent(id)}`, json("PATCH", { paused }));
export const archiveTwitchChannel = (id: string) => request<TwitchChannel>(`/twitch/channels/${encodeURIComponent(id)}`, { method: "DELETE" });
export const fetchTwitchClips = () => request<TwitchClip[]>("/twitch/clips?limit=100");
export const fetchTwitchStatus = () => request<TwitchCollectorStatus>("/twitch/status");
export const checkTwitchNow = () => request<{ accepted: boolean }>("/twitch/check", json("POST", {}));
export const fetchImports = (state = "") => request<ImportedClip[]>(`/imports${state ? `?state=${encodeURIComponent(state)}` : ""}`);
export const rejectImport = (id: string) => request<ImportedClip>(`/imports/${encodeURIComponent(id)}`, json("PATCH", { rejected: true }));
export const fetchCompilations = () => request<Compilation[]>("/compilations");
export const fetchCompilation = (id: string) => request<Compilation>(`/compilations/${encodeURIComponent(id)}`);
export const createCompilation = (spec: CompilationRequest) => request<Compilation>("/compilations", json("POST", spec));
export const saveCompilation = (id: string, version: number, spec: CompilationRequest) =>
  request<Compilation>(`/compilations/${encodeURIComponent(id)}`, json("PUT", { expected_version: version, ...spec }));
export const triggerAutoCompilation = () => request<AutoCompilationResult>("/compilations/auto", json("POST", {}));
