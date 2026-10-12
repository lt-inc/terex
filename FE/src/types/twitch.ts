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
  compilation_budget_percent?: number;
  admission_mode?: "shadow" | "enforce";
}

export interface TwitchClipGrowth {
  state: "warming" | "measured" | "counter_reset" | "stale";
  delta_views?: number;
  interval_sec?: number;
  views_per_hour?: number;
  observed_at?: string;
  anomalous?: boolean;
}

export interface TwitchDiscoveryCoverage {
	 as_of?: string;
	 refresh_clips?: number;
  windows_total: number;
  windows_completed: number;
  requests: number;
  complete: boolean;
  reason?: string;
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
  language?: string;
  language_source?: string;
  game_id?: string;
  thumbnail_url?: string;
  growth?: TwitchClipGrowth;
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
  block_reason?: string;
  last_check_at?: string | null;
  last_error?: string;
  channels: number;
  queued: number;
  waiting_assignment?: number;
  verification_pending?: number;
  verifying?: number;
  oldest_authorized_at?: string;
  oldest_verification_at?: string;
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
  excluded?: boolean;
  created_at: string;
  error?: string;
  duration_sec?: number;
  size_bytes?: number;
  width?: number;
  height?: number;
  flow_id?: string;
  media_id?: string;
  media_state?: string;
}

export interface TwitchMetadataSettings {
  short_intro_template?: string;
  compilation_intro_template?: string;
  hashtag_mode?: "standard" | "custom";
  custom_hashtags?: string[];
}

export interface TwitchMetadataSnapshot {
  schema_version: number;
  format: "short" | "compilation";
  settings: TwitchMetadataSettings;
  source_mode: string;
  category_name?: string;
  language_filter?: string;
  language: string;
  sources: { login: string; language?: string; language_source?: string }[];
  intro: string;
  intro_edited: boolean;
}

export interface TwitchMetadataPreview {
  intro: string;
  description: string;
  hashtags: string[];
  language: string;
  errors: string[];
  description_bytes: number;
  tag_characters: number;
  snapshot: TwitchMetadataSnapshot;
}

export interface TwitchMetadataPreviewRequest {
  flow_id?: string;
  format: "short" | "compilation";
  title: string;
  settings?: TwitchMetadataSettings;
  source_mode?: string;
  category_name?: string;
  language_filter?: string;
  source_ids?: string[];
  intro?: string;
  intro_edited?: boolean;
  clip_id?: string;
  revision_id?: number;
}

export interface TwitchFlow {
  id: string;
  name: string;
  enabled: boolean;
  archived?: boolean;
  schedule_horizon_hours: number;
  max_source_age_hours: number;
  auto_publish: boolean;
  account_id: number;
  youtube_channel_id: string;
  version: number;
  source_mode: "channels" | "category";
  broadcaster_ids: string[];
  channel_inputs?: string[];
  excluded_source_ids?: string[];
  category_id?: string;
  category_name?: string;
  language?: string;
  metadata?: TwitchMetadataSettings;
  poll_interval_minutes: number;
  ranking_mode?: "views" | "growth";
  window_hours: number;
  min_views: number;
  min_age_minutes: number;
  min_duration_sec: number;
  max_duration_sec: number;
  shorts_enabled: boolean;
  compilations_enabled: boolean;
  compilation_min_duration_sec: number;
  compilation_max_duration_sec: number;
  compilation_min_clips: number;
  compilation_max_clips: number;
  compilation_hour: number;
  balance_by_time: boolean;
  shorts_slots: string[];
  compilation_slots: string[];
  timezone: string;
  daily_shorts: number;
  daily_compilations: number;
  legacy_length?: boolean;
  created_at?: string;
  updated_at?: string;
  work_started_at?: string;
  last_checked_at?: string;
  last_error?: string;
  block_reason?: string;
  found_clips?: number;
  ready_outputs?: number;
  next_publication_at?: string;
}

export interface TwitchFlowClip {
  source: TwitchSource;
  source_state: string;
  reason?: {
    code: string;
    message: string;
    next_attempt_at?: string;
    started_at?: string;
    attempt?: number;
    actions?: string[];
  };
  import_id: string;
  source_clip_id?: string;
  media_id?: string;
  media_available?: boolean;
  keep?: boolean;
  excluded?: boolean;
  rejected?: boolean;
  outputs?: {
    clip_id: string;
    revision_id: number;
    format: "short" | "compilation" | "shorts";
    render_state: string;
    approval: string;
    publication_state?: string;
    publish_at?: string;
    error?: string;
  }[];
}

export interface TwitchCapacity {
  flow_id: string;
  youtube_channel_id: string;
  horizon_hours: number;
  horizon_end: string;
  mode: "preview" | "enforce";
  formats: {
    format: string;
    total_slots: number;
    occupied_slots: number;
    preparing_slots: number;
    free_slots: number;
    next_slot_at?: string;
    reason?: string;
    next_check_at?: string;
    source_count?: number;
    source_duration_sec?: number;
    missing_clips?: number;
    missing_duration_sec?: number;
  }[];
}

export interface TwitchPolicyPreview {
  mode: "preview" | "enforce";
  violations: {
    publication_id: number;
    clip_id: string;
    flow_id: string;
    automatic: boolean;
    publish_at: string;
    source_created_at?: string;
    deadline?: string;
    code: string;
    message: string;
    protected: boolean;
  }[];
}

export interface TwitchStorageItem {
  bucket: string;
  object: string;
  kind: string;
  size_bytes: number;
  state: string;
  keep: boolean;
  pins?: { kind: "import" | "clip"; id: string }[];
  expires_at?: string;
  protected_reason?: string;
  error?: string;
  clip_ids: string[];
  import_ids: string[];
}
export interface TwitchStorage {
  pressure?: {
    required: boolean;
    state: "ok" | "low" | "unknown" | "disabled";
    available_bytes: number;
    capacity_bytes: number;
    observed_at?: string;
    reason: string;
  };
  cleanup_enabled: boolean;
  inventory_complete: boolean;
  physical_bytes: number;
  reclaimable_bytes: number;
  objects: number;
  protected_objects: number;
  deleted_objects: number;
  pending_deletions: number;
  failed_deletions: number;
  updated_at?: string;
  items: TwitchStorageItem[];
  candidates?: TwitchStorageItem[];
}
export interface TwitchFlowClipPage {
  items: TwitchFlowClip[];
  total: number;
  limit: number;
  offset: number;
}

export interface CompilationItem {
  clip_id: string;
  revision_id: number;
  source_id?: string;
  start_sec: number;
  end_sec: number;
}

export interface CompilationRequest {
  title: string;
  flow_id?: string;
  items: CompilationItem[];
}

export interface Compilation extends CompilationRequest {
  flow_config?: TwitchFlow;
  clip_id: string;
  revision_id: number;
  version: number;
  state: string;
  approved_at?: string | null;
  preview_url?: string;
  error?: string;
  created_at: string;
  duration_sec?: number;
}

export interface AutoCompilationResult {
  created: boolean;
  compilation: Compilation | null;
  reason?:
    | "created"
    | "target_account_missing"
    | "collection_disabled"
    | "before_scheduled_hour"
    | "daily_draft_exists"
    | "insufficient_eligible_sources";
  message?: string;
  local_date?: string;
  timezone?: string;
  scheduled_at?: string;
  next_attempt_at?: string;
  minimum_clips?: number;
  window_hours?: number;
  sources?: {
    total: number;
    eligible: number;
    rejected: number;
    not_ready: number;
    download_pending?: number;
    downloading?: number;
    downloading_known?: boolean;
    outside_window: number;
    already_used: number;
  };
  ready_duration_sec?: number;
  minimum_duration_sec?: number;
  missing_duration_sec?: number;
  selected_clips?: number;
}

export interface TwitchFlowDiagnostics {
  flow_id: string;
  generated_at: string;
  compilation: AutoCompilationResult & { selected_clips: number; ready_duration_sec: number; minimum_duration_sec: number; missing_duration_sec: number; minimum_clips: number; scheduled_at: string };
  capacity: TwitchCapacity;
  sources: Record<string, number>;
  competitors: { flow_id: string; name: string; format: string; scheduled: number; preparing: number; nearest_at?: string }[];
  collector: { available: boolean; message?: string; spent: number; daily_limit: number; daily_unlimited?: boolean; remaining: number; timezone: string; compilation_budget_percent: number; admission_mode: string; by_flow: {flow_id: string; name: string; format: string; spent: number}[]; discovery?: {fetched:number;language_matched:number;language_rejected:number;language_unknown:number;page_limit_reached:boolean;checked_at:string;broadcaster_id:string;error?:string;coverage?:TwitchDiscoveryCoverage}[] };
  average_duration_sec: number;
  average_estimated: boolean;
  estimated_additional_clips: number;
  expected_clips: number;
  expected_duration_sec: number;
  feasibility: string;
  messages: string[];
}

export interface TwitchResetPreview {
  preview_token: string;
  expires_at: string;
  confirmation_phrase: string;
  counts: Record<string, number>;
  blockers: {code:string;message:string;publication_id?:number}[];
}
export interface TwitchResetStatus {
  id?: string;
  state: "idle" | "stopping" | "deleting" | "clearing" | "completed" | "failed";
  epoch?: number;
  collector_ack?: boolean;
  gpu_drained?: boolean;
  counts?: Record<string,number>;
  deleted_objects?: number;
  deleted_bytes?: number;
  remaining_objects?: number;
  blockers?: {code:string;message:string;publication_id?:number}[];
  error?: string;
  updated_at?: string;
}
