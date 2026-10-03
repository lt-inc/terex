export interface TwitchSettings {
  enabled: boolean;
  target_account_id: number;
  poll_interval_minutes: number;
  window_hours: number;
  first: number;
  min_views: number;
  min_age_minutes: number;
  min_duration_sec: number;
  max_duration_sec: number;
  daily_downloads: number;
  per_channel_daily_downloads: number;
  daily_shorts: number;
  daily_compilations: number;
  compilation_min_clips: number;
  compilation_max_clips: number;
  compilation_hour: number;
  backlog_limit: number;
  timezone: string;
}

export interface TwitchSource {
  platform: string;
  clip_id: string;
  broadcaster_id: string;
  broadcaster_login: string;
  title: string;
  url: string;
  created_at: string;
  observed_at: string;
  view_count: number;
  duration_sec: number;
  video_id?: string;
  vod_offset?: number | null;
}

export interface TwitchChannel {
  id: string;
  broadcaster_id: string;
  login: string;
  display_name: string;
  paused: boolean;
  archived: boolean;
  created_at: string;
  last_checked_at?: string | null;
  last_error?: string;
}

export interface TwitchClip {
  id: string;
  channel_id: string;
  broadcaster_id: string;
  broadcaster_name: string;
  title: string;
  view_count: number;
  created_at: string;
  duration: number;
  video_id?: string;
  vod_offset?: number | null;
  url: string;
  state: string;
  last_error?: string;
  import_id?: string;
}

export interface TwitchCollectorStatus {
  running: boolean;
  paused: boolean;
  credentials_ready: boolean;
  last_check_at?: string | null;
  last_error?: string;
  channels: number;
  queued: number;
  downloading: number;
  imported: number;
  failed: number;
}

export interface ImportedClip {
  import_id: string;
  source: TwitchSource;
  state: string;
  account_id: number;
  job_id?: string;
  clip_id?: string;
  revision_id?: number;
  rejected: boolean;
  created_at: string;
  error?: string;
  duration_sec?: number;
  size_bytes?: number;
  width?: number;
  height?: number;
}

export interface CompilationItem {
  clip_id: string;
  revision_id: number;
  start_sec: number;
  end_sec: number;
}

export interface CompilationRequest {
  title: string;
  items: CompilationItem[];
}

export interface Compilation extends CompilationRequest {
  clip_id: string;
  revision_id: number;
  version: number;
  state: string;
  approved_at?: string | null;
  preview_url?: string;
  error?: string;
  created_at: string;
}
