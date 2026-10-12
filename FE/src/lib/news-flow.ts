import type { NewsFlow, NewsImageMode, NewsPostStatus } from "@/types/news";

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
  image_mode: "auto",
  use_emoji: true,
  use_hashtags: true,
  footer: "",
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
  if (flow.footer.length > 300) return "Подпись внизу: не длиннее 300 символов.";
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

export const IMAGE_MODE_LABEL: Record<NewsImageMode, string> = {
  auto: "Авто: картинка из новости, иначе стоковое фото",
  article: "Только из самой новости",
  web: "Стоковое фото из интернета по теме",
  none: "Без картинки",
};

export type Inline = { text: string; bold?: boolean; italic?: boolean };
export type Block = { kind: "p" | "quote" | "bullet"; inlines: Inline[] };

// Light post markup: **bold**, __italic__, "- " bullets, "> " quotes. Mirrors the collector's renderer,
// so the panel preview shows what Telegram will show. Nothing is ever treated as HTML.
export function parseInlines(line: string): Inline[] {
  const out: Inline[] = [];
  for (const part of line.split(/(\*\*.+?\*\*|__.+?__)/)) {
    if (!part) continue;
    if (part.startsWith("**") && part.endsWith("**") && part.length > 4) out.push({ text: part.slice(2, -2), bold: true });
    else if (part.startsWith("__") && part.endsWith("__") && part.length > 4) out.push({ text: part.slice(2, -2), italic: true });
    else out.push({ text: part.replaceAll("**", "").replaceAll("__", "") });
  }
  return out;
}

export function parseMarkup(text: string): Block[] {
  const blocks: Block[] = [];
  for (const raw of text.replaceAll("\r\n", "\n").split("\n")) {
    const line = raw.trimEnd();
    const t = line.trimStart();
    if (t.startsWith("> ")) {
      const last = blocks[blocks.length - 1];
      const inlines = parseInlines(t.slice(2));
      if (last?.kind === "quote") last.inlines.push({ text: "\n" }, ...inlines);
      else blocks.push({ kind: "quote", inlines });
    } else if (t.startsWith("- ") || t.startsWith("* ") || t.startsWith("• ")) {
      blocks.push({ kind: "bullet", inlines: parseInlines(t.slice(2)) });
    } else if (t === "") {
      blocks.push({ kind: "p", inlines: [] });
    } else {
      blocks.push({ kind: "p", inlines: parseInlines(line) });
    }
  }
  return blocks;
}

// Calendar helpers -----------------------------------------------------------------------------

// The moment a post sits at in the calendar: when it went out, else when it is queued to go out.
export function postWhen(p: { published_at: string | null; scheduled_at: string | null }): string | null {
  return p.published_at ?? p.scheduled_at;
}

export const CALENDAR_TONE: Record<NewsPostStatus, string> = {
  published: "border-emerald-700 bg-emerald-950/40",
  failed: "border-red-700 bg-red-950/40",
  approved: "border-indigo-700 bg-indigo-950/40",
  pending: "border-amber-700 bg-amber-950/40",
  rejected: "border-slate-700 bg-slate-800/60 opacity-60",
  expired: "border-slate-700 bg-slate-800/60 opacity-60",
};

// Public channels (@name) have post links of the form https://t.me/<name>/<message id>.
export function telegramPostURL(chatID: string, messageID: number | null): string | null {
  if (!messageID || !/^@[A-Za-z][A-Za-z0-9_]{3,31}$/.test(chatID)) return null;
  return `https://t.me/${chatID.slice(1)}/${messageID}`;
}
