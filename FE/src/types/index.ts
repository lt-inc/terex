export type Platform = "youtube" | "tiktok" | "instagram";

export interface PublishSchedule {
  date: string;
  time: string;
  step: string;
}

export interface BannerConfig {
  id: string;
  bannerName: string;
  youtubeAccounts: string[];
  tiktokAccounts: string[];
  instagramAccounts: string[];
  schedules: Partial<Record<Platform, PublishSchedule>>;
}

export interface VideoCardData {
  id: string;
  youtubeUrl: string;
  makeVertical: boolean;
  addSubtitles: boolean;
  sliceVideo: boolean;
  addBackground: boolean;
  shape: "circle" | "rectangle";
  backgroundName: string;
  // base account selection (always visible)
  youtubeAccounts: string[];
  tiktokAccounts: string[];
  instagramAccounts: string[];
  schedules: Partial<Record<Platform, PublishSchedule>>;
  banners: BannerConfig[];
}

export interface AccountsResponse {
  youtube: string[];
  tiktok: string[];
  instagram: string[];
}

export interface AccountCardData {
  id: string;
  platform: string;
  name: string;
  url: string;
  authFile: File | null;
  note: string;
}

export interface AssetCardData {
  id: string;
  name: string;
  file: File | null;
  note: string;
}

export interface AccountRecord {
  id: number;
  platform: string;
  name: string;
  url: string | null;
  note: string | null;
  bucket: string | null;
  file_name: string | null;
}

export interface AssetRecord {
  id: number;
  name: string;
  note: string | null;
  bucket: string | null;
  file_name: string | null;
}

export interface SubJobData {
  sub_job_id: string;
  platform: string;
  publish_datetime: string | null;
  account_ids: number[];
  account_names: string[];
  banner_id: number | null;
  banner_name: string | null;
}

export interface JobData {
  job_id: string;
  youtube_url: string;
  is_vertical: boolean;
  is_subtitles: boolean;
  is_slice: boolean;
  is_background: boolean;
  is_circle: boolean;
  status: string;
  trace: string | null;
  create_datetime: string | null;
  last_change_datetime: string | null;
  sub_jobs: SubJobData[];
}
