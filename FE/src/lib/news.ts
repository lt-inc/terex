import { request } from "./api";
import type {
  NewsArticle,
  NewsChannel,
  NewsFeedPreview,
  NewsImageCandidate,
  NewsImageEdit,
  NewsFlow,
  NewsPost,
  NewsPostStatus,
  NewsSource,
  NewsStatus,
  NewsStory,
} from "@/types/news";

function json(method: string, data: unknown): RequestInit {
  return { method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(data) };
}
const base = "/news";

export const fetchNewsStatus = (signal?: AbortSignal) => request<NewsStatus>(`${base}/status`, { signal });

export const fetchNewsSources = (signal?: AbortSignal) => request<NewsSource[]>(`${base}/sources`, { signal });
export const createNewsSource = (source: Partial<NewsSource>) => request<NewsSource>(`${base}/sources`, json("POST", source));
export const updateNewsSource = (id: number, patch: Partial<NewsSource>) => request<NewsSource>(`${base}/sources/${id}`, json("PATCH", patch));
export const deleteNewsSource = (id: number) => request<{ ok: boolean }>(`${base}/sources/${id}`, { method: "DELETE" });
export const testNewsSource = (source: Partial<NewsSource>) => request<NewsFeedPreview>(`${base}/sources/test`, json("POST", source));
export const fetchNewsSourceNow = (id: number) => request<{ new_articles: number }>(`${base}/sources/${id}/fetch`, json("POST", {}));

export const fetchNewsChannels = (signal?: AbortSignal) => request<NewsChannel[]>(`${base}/channels`, { signal });
export const createNewsChannel = (input: { title: string; chat_id: string; bot_token: string }) =>
  request<NewsChannel>(`${base}/channels`, json("POST", input));
export const updateNewsChannel = (id: number, patch: { title?: string; chat_id?: string; bot_token?: string }) =>
  request<NewsChannel>(`${base}/channels/${id}`, json("PATCH", patch));
export const deleteNewsChannel = (id: number) => request<{ ok: boolean }>(`${base}/channels/${id}`, { method: "DELETE" });
export const verifyNewsChannel = (id: number) => request<NewsChannel>(`${base}/channels/${id}/verify`, json("POST", {}));
export const testNewsChannel = (id: number) => request<{ ok: boolean }>(`${base}/channels/${id}/test`, json("POST", {}));

export const fetchNewsFlows = (signal?: AbortSignal) => request<NewsFlow[]>(`${base}/flows`, { signal });
export const fetchNewsFlow = (id: number, signal?: AbortSignal) => request<NewsFlow>(`${base}/flows/${id}`, { signal });
export const createNewsFlow = (flow: Partial<NewsFlow>) => request<NewsFlow>(`${base}/flows`, json("POST", flow));
export const updateNewsFlow = (id: number, patch: Partial<NewsFlow>) => request<NewsFlow>(`${base}/flows/${id}`, json("PATCH", patch));
export const deleteNewsFlow = (id: number) => request<{ ok: boolean }>(`${base}/flows/${id}`, { method: "DELETE" });
export const generateNewsPost = (id: number) => request<NewsPost | null>(`${base}/flows/${id}/generate`, json("POST", {}));
export const fetchNewsStories = (id: number, signal?: AbortSignal) => request<NewsStory[]>(`${base}/flows/${id}/stories`, { signal });
export const fetchNewsArticles = (flowID = 0, signal?: AbortSignal) =>
  request<NewsArticle[]>(`${base}/articles?limit=50${flowID ? `&flow_id=${flowID}` : ""}`, { signal });

export const fetchNewsPosts = (status: NewsPostStatus | "" = "", flowID = 0, signal?: AbortSignal) => {
  const query = new URLSearchParams({ limit: "100" });
  if (status) query.set("status", status);
  if (flowID) query.set("flow_id", String(flowID));
  return request<NewsPost[]>(`${base}/posts?${query}`, { signal });
};
export const editNewsPost = (
  id: number,
  patch: { title?: string; text?: string; emoji?: string; hashtags?: string; image?: NewsImageEdit },
) => request<NewsPost>(`${base}/posts/${id}`, json("PATCH", patch));
export const fetchNewsPostImages = (id: number, query = "", signal?: AbortSignal) =>
  request<NewsImageCandidate[]>(`${base}/posts/${id}/images${query ? `?query=${encodeURIComponent(query)}` : ""}`, { signal });
export const approveNewsPost = (id: number, scheduledAt?: string) =>
  request<NewsPost>(`${base}/posts/${id}/approve`, json("POST", scheduledAt ? { scheduled_at: scheduledAt } : {}));
export const rejectNewsPost = (id: number) => request<NewsPost>(`${base}/posts/${id}/reject`, json("POST", {}));
export const previewNewsPost = (id: number) => request<{ html: string }>(`${base}/posts/${id}/preview`);
