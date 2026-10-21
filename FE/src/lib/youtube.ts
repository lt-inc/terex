import { request } from "./api";
import type { YTCandidate, YTCandidateState, YTFlow, YTFlowConfig, YTQuota } from "@/types/youtube";

function json(method: string, data: unknown): RequestInit {
  return { method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(data) };
}
const base = "/youtube";

export const DEFAULT_YT_CONFIG: YTFlowConfig = {
  window_hours: 72,
  all_time: false,
  max_videos: 200,
  min_views: 5000,
  min_views_per_hour: 0,
  min_duration_sec: 3,
  max_duration_sec: 180,
  slots: ["09:00", "12:00", "15:00", "18:00", "21:00"],
  daily_limit: 0,
  daily_downloads: 0,
  approve_mode: "by_approve",
  quota_daily: 1000,
  refresh_hours: 6,
  scan_hours: 6,
  brand_style: false,
  brand_background: "",
  brand_banner: "",
  brand_subtitles: true,
};

export const fetchYTFlows = (signal?: AbortSignal) => request<YTFlow[]>(`${base}/flows`, { signal });
export const fetchYTFlow = (id: string, signal?: AbortSignal) => request<YTFlow>(`${base}/flows/${id}`, { signal });
export const createYTFlow = (flow: { name: string; account_id: number; enabled: boolean; config: YTFlowConfig }) =>
  request<YTFlow>(`${base}/flows`, json("POST", flow));
export const updateYTFlow = (id: string, patch: Partial<Pick<YTFlow, "name" | "account_id" | "enabled" | "config">>) =>
  request<YTFlow>(`${base}/flows/${id}`, json("PATCH", patch));
export const deleteYTFlow = (id: string) => request<{ deleted: boolean }>(`${base}/flows/${id}`, { method: "DELETE" });
export const runYTFlow = (id: string) => request<YTFlow>(`${base}/flows/${id}/run`, json("POST", {}));
export const addYTSource = (id: string, source: { kind: "channel" | "search"; value: string; region_code?: string; language?: string }) =>
  request(`${base}/flows/${id}/sources`, json("POST", source));
export const deleteYTSource = (id: string, sourceId: number) => request(`${base}/flows/${id}/sources/${sourceId}`, { method: "DELETE" });
export const fetchYTCandidates = (id: string, state: YTCandidateState | "" = "", signal?: AbortSignal) =>
  request<YTCandidate[]>(`${base}/flows/${id}/candidates?limit=100${state ? `&state=${state}` : ""}`, { signal });
export const skipYTCandidate = (id: string, video: string) => request(`${base}/flows/${id}/candidates/${video}/skip`, json("POST", {}));
export const fetchYTQuota = (signal?: AbortSignal) => request<YTQuota>(`${base}/quota`, { signal });

export const STATUS_LABEL: Record<string, string> = {
  ok: "Работает",
  waiting: "Ждёт свободного окна",
  quota: "Квота API исчерпана",
  budget: "Бюджет потока исчерпан",
  no_account: "Нет рабочего YouTube-аккаунта",
  error: "Ошибка",
  "": "Ещё не запускался",
};

export const STATE_LABEL: Record<YTCandidateState, string> = {
  new: "Ждут слота",
  queued: "В работе",
  done: "Опубликованы",
  failed: "Ошибки",
  skipped: "Пропущены",
};

export function validateSlots(text: string): { slots: string[]; error?: string } {
  const slots = text.split(/[\s,;]+/).filter(Boolean);
  const bad = slots.find((s) => !/^([01]\d|2[0-3]):[0-5]\d$/.test(s));
  return bad ? { slots, error: `Окно «${bad}» должно выглядеть как 09:00` } : { slots };
}
