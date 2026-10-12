export type NewsApprove = "by_approve" | "auto";
export type NewsLanguage = "ru" | "en";
export type NewsPostStatus = "pending" | "approved" | "published" | "failed" | "rejected" | "expired";

export interface NewsSource {
  id: number;
  name: string;
  kind: "rss" | "gnews";
  url: string;
  query: string;
  lang: NewsLanguage;
  enabled: boolean;
  fetch_interval_min: number;
  last_fetched_at: string | null;
  last_error: string;
  articles: number;
  created_at: string;
}

export interface NewsChannel {
  id: number;
  title: string;
  chat_id: string;
  bot_username: string;
  chat_title: string;
  status: "unverified" | "ok" | "error";
  last_error: string;
  verified_at: string | null;
  has_token: boolean;
  created_at: string;
}

export interface NewsFlow {
  id: number;
  name: string;
  enabled: boolean;
  topic: string;
  language: NewsLanguage;
  channel_id: number | null;
  approve_mode: NewsApprove;
  posts_per_day: number;
  window_start_hour: number;
  window_end_hour: number;
  timezone: string;
  min_interval_min: number;
  min_sources: number;
  min_relevance: number;
  window_hours: number;
  max_pending: number;
  style_prompt: string;
  include_link: boolean;
  max_chars: number;
  model: string;
  source_ids: number[];
  last_generated_at: string | null;
  last_error: string;
  channel_title?: string;
  pending_posts?: number;
  posted_today?: number;
  open_clusters?: number;
}

export interface NewsLink {
  title: string;
  url: string;
}

export interface NewsPost {
  id: number;
  flow_id: number;
  flow_name?: string;
  cluster_id: number | null;
  channel_id: number | null;
  title: string;
  text: string;
  links: NewsLink[];
  status: NewsPostStatus;
  scheduled_at: string | null;
  published_at: string | null;
  message_id: number | null;
  error: string;
  attempts: number;
  score: number;
  created_at: string;
}

export interface NewsStory {
  id: number;
  title: string;
  relevance: number;
  sources: number;
  articles: number;
  first_at: string;
  last_at: string;
  score: number;
}

export interface NewsArticle {
  id: number;
  source_name: string;
  publisher: string;
  url: string;
  title: string;
  lang: string;
  published_at: string;
  status: "new" | "extracted" | "ready" | "duplicate" | "failed";
}

export interface NewsStatus {
  llm_ready: boolean;
  secret_key_ready: boolean;
  flows: number;
  enabled_flows: number;
  sources: number;
  channels: number;
  articles_24h: number;
  unembedded: number;
  pending_posts: number;
  last_fetch_at: string | null;
  last_tick_at: string | null;
  last_tick_error: string;
}

export interface NewsFeedPreview {
  title: string;
  items: { title: string; link: string; published: string }[];
}
