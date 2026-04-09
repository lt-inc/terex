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
  minVideoDuration: number;
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
  capturedFileName: string | null;
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

export interface OutData {
  id: number;
  video_name: string | null;
  name: string | null;
  description: string | null;
  hashtag: string | null;
  upload_id: string | null;
  publish_date: string | null;
  published_at: string | null;
  status: string | null;
  account_id: number | null;
  account_name: string | null;
  platform: string | null;
}

export interface SubJobData {
  sub_job_id: string;
  publish_datetime: string | null;
  status: string;
  fail_place: string;
  account_ids: number[];
  account_names: string[];
  is_add_banner: boolean;
  banner_id: number | null;
  banner_name: string | null;
  outs: OutData[];
}

export interface WarmupJob {
  job_id: string;
  account_id: number;
  account_name: string | null;
  account_platform: string | null;
  status: string;
  type: string;
  warm_up_datetime: string | null;
  repeat_step: number | null;
  max_videos: string;
  is_archived: boolean;
}

export interface JobData {
  job_id: string;
  youtube_url: string;
  is_vertical: boolean;
  is_subtitles: boolean;
  is_slice: boolean;
  is_background: boolean;
  min_video_duration: number;
  background_id: number | null;
  background_name: string | null;
  is_circle: boolean;
  status: string;
  trace: string | null;
  is_archived: boolean;
  create_datetime: string | null;
  last_change_datetime: string | null;
  sub_jobs: SubJobData[];
}
