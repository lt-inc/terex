export type YTApproveMode = "auto_approve" | "by_approve";
export type YTCandidateState = "new" | "queued" | "done" | "failed" | "skipped";

export interface YTFlowConfig {
  window_hours: number;
  all_time: boolean;
  max_videos: number;
  min_views: number;
  min_views_per_hour: number;
  min_duration_sec: number;
  max_duration_sec: number;
  slots: string[];
  daily_limit: number;
  daily_downloads: number;
  approve_mode: YTApproveMode;
  quota_daily: number;
  refresh_hours: number;
  scan_hours: number;
  brand_style: boolean;
  brand_background: string; // "" = blur of the video
  brand_banner: string; // "" = no banner
}

export interface YTSource {
  id: number;
  flow_id: string;
  kind: "channel" | "search";
  value: string;
  title: string;
  params: { region_code?: string; language?: string };
  last_rss_at: string | null;
  last_scan_at: string | null;
  last_error?: string;
}

export interface YTFlow {
  id: string;
  name: string;
  account_id: number;
  account_name: string;
  channel_id: string;
  enabled: boolean;
  config: YTFlowConfig;
  version: number;
  last_run_at: string | null;
  last_status: string;
  last_error?: string;
  sources: YTSource[];
  counts: { new: number; queued: number; done: number; failed: number; skipped: number };
  quota_used: number;
  next_slot_at: string | null;
}

export interface YTCandidate {
  flow_id: string;
  video_id: string;
  channel_id: string;
  channel_title: string;
  title: string;
  published_at: string | null;
  duration_sec: number;
  views: number;
  likes: number | null;
  state: YTCandidateState;
  reason?: string;
  job_id: string | null;
  slot_at: string | null;
  score: number;
}

export interface YTQuota {
  day: string;
  pool: number;
  used: number;
  by_flow: Record<string, number>;
  tripped: boolean;
  trip_note?: string;
}
