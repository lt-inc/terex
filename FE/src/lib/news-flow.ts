import type { NewsFlow, NewsPostStatus } from "@/types/news";

export const DEFAULT_NEWS_FLOW: Omit<NewsFlow, "id"> = {
  name: "",
  enabled: false,
  topic: "",
  language: "ru",
  channel_id: null,
  approve_mode: "by_approve",
  posts_per_day: 3,
  window_start_hour: 9,
  window_end_hour: 22,
  timezone: "Europe/Moscow",
  min_interval_min: 60,
  min_sources: 1,
  min_relevance: 0.25,
  window_hours: 24,
  max_pending: 5,
  style_prompt: "",
  include_link: true,
  max_chars: 900,
  model: "",
  source_ids: [],
  last_generated_at: null,
  last_error: "",
};

export const NEWS_TIMEZONES = [
  "Europe/Moscow",
  "Europe/Kaliningrad",
  "Europe/Samara",
  "Asia/Yekaterinburg",
  "Asia/Novosibirsk",
  "Asia/Tomsk",
  "Asia/Vladivostok",
  "Europe/Kyiv",
  "Europe/London",
  "America/New_York",
  "UTC",
];

function whole(value: unknown, min: number, max: number): boolean {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= min && value <= max;
}

// Mirrors the collector's validation so the form can explain a problem before sending it.
export function validateNewsFlow(flow: Omit<NewsFlow, "id"> | NewsFlow): string | null {
  if (!flow.name.trim()) return "Укажите название потока.";
  if (!whole(flow.posts_per_day, 1, 48)) return "Постов в день: целое число от 1 до 48.";
  if (!whole(flow.window_start_hour, 0, 23) || !whole(flow.window_end_hour, 1, 24) || flow.window_start_hour >= flow.window_end_hour)
    return "Часы публикации: начало меньше конца (0–24).";
  if (!whole(flow.min_interval_min, 0, 1440)) return "Минимальный интервал: 0–1440 минут.";
  if (!whole(flow.min_sources, 1, 20)) return "Минимум источников в сюжете: от 1 до 20.";
  if (typeof flow.min_relevance !== "number" || !(flow.min_relevance >= 0 && flow.min_relevance <= 1)) return "Релевантность теме: число от 0 до 1.";
  if (!whole(flow.window_hours, 1, 168)) return "Окно свежести: 1–168 часов.";
  if (!whole(flow.max_pending, 1, 50)) return "Лимит черновиков на ревью: 1–50.";
  if (!whole(flow.max_chars, 200, 3500)) return "Длина поста: 200–3500 символов.";
  if (/\s/.test(flow.model.trim())) return "Идентификатор модели не должен содержать пробелов.";
  if (flow.enabled && flow.channel_id == null) return "Выберите Telegram-канал, прежде чем включать поток.";
  if (flow.enabled && flow.source_ids.length === 0) return "Выберите хотя бы один источник, прежде чем включать поток.";
  return null;
}

export const POST_STATUS_LABEL: Record<NewsPostStatus, string> = {
  pending: "На ревью",
  approved: "В очереди",
  published: "Опубликован",
  failed: "Ошибка",
  rejected: "Отклонён",
  expired: "Просрочен",
};

export const POST_STATUS_STYLE: Record<NewsPostStatus, string> = {
  pending: "bg-amber-500/15 text-amber-300",
  approved: "bg-sky-500/15 text-sky-300",
  published: "bg-emerald-500/15 text-emerald-300",
  failed: "bg-red-500/15 text-red-300",
  rejected: "bg-slate-600/40 text-slate-300",
  expired: "bg-slate-600/40 text-slate-400",
};

// Why a flow is not producing posts, in the order an operator should fix it.
export function flowBlockers(flow: NewsFlow, llmReady: boolean): string[] {
  const out: string[] = [];
  if (!llmReady) out.push("Не настроен OPENROUTER_API_KEY — генерация остановлена.");
  if (flow.channel_id == null) out.push("Не выбран Telegram-канал.");
  if (flow.source_ids.length === 0) out.push("Не выбраны источники.");
  if (!flow.enabled) out.push("Поток выключен.");
  return out;
}

export function relativeTime(iso: string | null, now = Date.now()): string {
  if (!iso) return "—";
  const ms = now - new Date(iso).getTime();
  if (!Number.isFinite(ms)) return "—";
  const s = Math.round(ms / 1000);
  if (s < 0) return "скоро";
  if (s < 60) return "только что";
  if (s < 3600) return `${Math.floor(s / 60)} мин назад`;
  if (s < 86400) return `${Math.floor(s / 3600)} ч назад`;
  return `${Math.floor(s / 86400)} дн назад`;
}
