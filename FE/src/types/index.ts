export type SliceMode = "highlights" | "full" | "full_clip" | "compilation";
export type ApproveMode = "by_approve" | "auto_approve";

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
  targets?: PublicationTarget[];
}

export interface WatermarkRegion {
  x: number;
  y: number;
  w: number;
  h: number;
}

// auto: detect overlays on every video; manual: blur the drawn regions (shares of the frame, 0..1)
export interface WatermarkOptions {
  mode: "off" | "auto" | "manual";
  regions?: WatermarkRegion[];
}

export interface WatermarkPreview {
  video_url: string;
  video_title?: string;
  width: number;
  height: number;
  frames: { time_sec: number; image: string }[];
  suggested: WatermarkRegion[];
}

export interface VideoCardData {
  id: string;
  youtubeUrl: string;
  makeVertical: boolean;
  addSubtitles: boolean;
  sliceMode: SliceMode;
  minVideoDuration: number;
  approveMode: ApproveMode;
  addBackground: boolean;
  shape: "circle" | "rectangle";
  backgroundName: string;
  // base account selection (always visible)
  youtubeAccounts: string[];
  tiktokAccounts: string[];
  instagramAccounts: string[];
  schedules: Partial<Record<Platform, PublishSchedule>>;
  banners: BannerConfig[];
  targets?: PublicationTarget[];
  watermark?: WatermarkOptions;
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
  source?: "file" | "youtube"; // backgrounds only; missing = file
  url?: string; // YouTube link when source is "youtube"
}

export interface AccountRecord {
  id: number;
  version?: number;
  platform: string;
  name: string;
  url: string | null;
  note: string | null;
  bucket: string | null;
  file_name: string | null;
  connection_state?: string;
  remote_account_id?: string;
  remote_account_name?: string;
  connection_error?: string;
  last_checked_at?: string | null;
  session_version?: number;
}

export interface TikTokConnection {
  account_id: number;
  status: string;
  proxy_mode: "default" | "direct" | "custom";
  proxy_display?: string;
  custom_proxy_configured?: boolean;
  pairing_token?: string;
  expires_at?: string;
  pairing_expires_at?: string;
  payload_url?: string;
  remote_account_id?: string;
  username?: string;
  display_name?: string;
  session_version?: number;
  error?: string;
  last_checked_at?: string;
}

export interface AssetRecord {
  id: number;
  name: string;
  note: string | null;
  bucket: string | null;
  file_name: string | null;
  preview_url?: string;
}

export interface OutData {
  id: number;
  job_id: string | null;
  sub_job_id: string | null;
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
  version?: number;
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

export interface JobData {
  job_id: string;
  youtube_url?: string;
  source_kind?: string;
  source_url?: string;
  source_id?: string;
  is_vertical: boolean;
  is_subtitles: boolean;
  slice_mode: SliceMode;
  approve_mode: ApproveMode;
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

export interface Channel {
  id: number;
  name: string;
  url: string;
  yt_channel_id: string;
  approve_mode: ApproveMode;
  enabled: boolean;
  spec: Partial<VideoCardData>;
  last_checked_at: string | null;
  feed_error_code?: string;
  last_feed_error?: string;
  next_feed_check_at?: string | null;
}

export interface Clip {
  clip_id: string;
  job_id: string;
  banner_object: string;
  bucket: string;
  object: string;
  idx: number;
  start_sec: number;
  end_sec: number;
  title: string;
  hashtags: string[];
  approval: "pending" | "approved" | "rejected";
  created_at: string;
  preview_url?: string;
  description?: string;
  current_revision?: number;
  render_state?: string;
}

export interface PublicationTarget {
  account_id: number;
  start_at?: string | null;
  interval_seconds: number;
  timezone: string;
}

export interface PublicationEvent {
  freshness_override?: boolean;
  twitch_policy?: {
    flow_id: string;
    source_created_at?: string;
    deadline?: string;
    horizon_end: string;
    max_source_age_hours: number;
    horizon_hours: number;
  };
  id: number;
  clip_id: string;
  account_id: number;
  account_name: string;
  platform: string;
  publish_at: string;
  published_at?: string | null;
  state: string;
  version: number;
  revision_id?: number;
  started_at?: string | null;
  uploaded_at?: string | null;
  processed_at?: string | null;
  cancel_requested?: boolean;
  title: string;
  description?: string;
  caption?: string;
  hashtags?: string[];
  upload_id?: string;
  external_url?: string;
  error?: string;
  error_code?: string;
  legacy?: boolean;
  history?: PublicationHistory[];
  status?: string;
  manual?: boolean;
}

export type DirectMediaKind = "rendered" | "source";
export type DirectPlatform = "youtube" | "tiktok";
export type DirectVisibility = "private" | "unlisted" | "public";

export interface DirectPublicationVariant {
  media_kind: DirectMediaKind;
  eligible: boolean;
  reason?: string;
}

export interface DirectPublicationOptions {
  revision_id: number;
  expected_version: number;
  title: string;
  caption: string;
  variants: DirectPublicationVariant[];
  publications: PublicationEvent[];
}

// Persisted verbatim while unconfirmed: a replay must send the same bytes.
export interface DirectPublishRequest {
  request_id: string;
  revision_id: number;
  expected_version: number;
  media_kind: DirectMediaKind;
  account_id: number;
  platform: DirectPlatform;
  caption: string;
  visibility: DirectVisibility;
  reviewed: true;
  freshness_override?: boolean;
}

export interface PublicationHistory {
	attempt_id?: string;
  id: string;
  created_at: string;
  kind: string;
  state: string;
  detail?: unknown;
}

export interface SubtitleCue {
  id: string;
  start: number;
  end: number;
  text: string;
}
export interface ClipEditor {
	metadata_snapshot?: import("./twitch").TwitchMetadataSnapshot;
	visibility?: "public" | "unlisted" | "private";
  clip_id: string;
  version: number;
  start_sec: number;
  end_sec: number;
  title: string;
  description: string;
  hashtags: string[];
  cues: SubtitleCue[];
  subtitles_enabled: boolean;
  source_url?: string;
  source_available?: boolean;
  source_error?: string;
  source_duration_sec?: number;
  rendered_preview_url?: string;
  revision_id: number;
  approved_revision_id?: number | null;
  approved_at?: string | null;
  error?: string | null;
  render_state: string;
  render_error?: string;
  approval: string;
  approved_revision?: number | null;
  revisions?: {
    revision_id: number;
    version: number;
    render_state: string;
    created_at?: string;
  }[];
}
