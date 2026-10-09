import type { TwitchFlow } from "@/types/twitch";
export const defaultTwitchFlow = (): TwitchFlow => ({
  id: "",
  name: "",
  enabled: false,
  archived: false,
  schedule_horizon_hours: 48,
  max_source_age_hours: 72,
  auto_publish: false,
  account_id: 0,
  youtube_channel_id: "",
  version: 0,
  source_mode: "channels",
  broadcaster_ids: [],
  channel_inputs: [],
  language: "",
  ranking_mode: "views",
  poll_interval_minutes: 144,
  window_hours: 72,
  min_views: 100,
  min_age_minutes: 60,
  min_duration_sec: 5,
  max_duration_sec: 60,
  shorts_enabled: true,
  compilations_enabled: true,
  compilation_min_duration_sec: 480,
  compilation_max_duration_sec: 720,
  compilation_min_clips: 3,
  compilation_max_clips: 60,
  compilation_hour: 21,
  balance_by_time: true,
  shorts_slots: ["09:00", "12:00", "15:00", "18:00", "21:00"],
  compilation_slots: ["21:30"],
  timezone: "Asia/Tomsk",
  daily_shorts: 5,
  daily_compilations: 1,
});
export const parseSlots = (value: string) =>
  [
    ...new Set(
      value
        .split(/[\s,;]+/)
        .map((item) => item.trim())
        .filter(Boolean),
    ),
  ].sort();
export function validateTwitchFlow(
  flow: TwitchFlow,
  step?: number,
): string | null {
  if (step == null || step === 0) {
    if (!flow.name.trim() || flow.name.length > 100)
      return "Название потока должно содержать 1–100 символов.";
    if (flow.account_id <= 0)
      return "Выберите подключённый и подтверждённый YouTube-канал.";
  }
  if (step == null || step === 1) {
    if (flow.ranking_mode && !["views", "growth"].includes(flow.ranking_mode))
      return "Выберите рейтинг по просмотрам или по росту.";
    if (
      flow.source_mode === "channels" &&
      !(flow.channel_inputs?.length || flow.broadcaster_ids.length) &&
      !(flow.id === "default" && flow.legacy_length)
    )
      return "Добавьте хотя бы один Twitch-канал.";
    if (flow.source_mode === "category" && !flow.category_id)
      return "Найдите и выберите категорию Twitch.";
    for (const [field, min, max] of [
      ["poll_interval_minutes", 1, 10080],
      ["window_hours", 1, 720],
      ["min_views", 0, Number.MAX_SAFE_INTEGER],
      ["min_age_minutes", 0, Number.MAX_SAFE_INTEGER],
    ] as const) {
      if (
        !Number.isSafeInteger(flow[field]) ||
        flow[field] < min ||
        flow[field] > max
      )
        return "Частота, просмотры и возраст должны быть целыми числами в допустимом диапазоне.";
    }
    if (flow.min_age_minutes >= flow.window_hours * 60)
      return "Минимальный возраст должен быть меньше окна поиска.";
    if (
      !Number.isFinite(flow.min_duration_sec) ||
      !Number.isFinite(flow.max_duration_sec) ||
      flow.min_duration_sec <= 0 ||
      flow.min_duration_sec > flow.max_duration_sec ||
      flow.max_duration_sec > 180
    )
      return "Длительность клипа: больше 0 и не более 180 секунд; минимум не выше максимума.";
  }
  if (step == null || step === 2) {
    if (!flow.shorts_enabled && !flow.compilations_enabled)
      return "Включите хотя бы один формат выпуска.";
    if (flow.compilations_enabled) {
      if (
        !Number.isSafeInteger(flow.compilation_min_clips) ||
        !Number.isSafeInteger(flow.compilation_max_clips) ||
        flow.compilation_min_clips < 3 ||
        flow.compilation_max_clips > 60 ||
        flow.compilation_max_clips < flow.compilation_min_clips
      )
        return "Подборка должна содержать 3–60 клипов; максимум не ниже минимума.";
      if (
        !flow.legacy_length &&
        (!Number.isFinite(flow.compilation_min_duration_sec) ||
          !Number.isFinite(flow.compilation_max_duration_sec) ||
          flow.compilation_min_duration_sec <= 0 ||
          flow.compilation_max_duration_sec < flow.compilation_min_duration_sec)
      )
        return "Минимальная длина подборки должна быть больше нуля и не превышать максимальную.";
      if (
        !Number.isSafeInteger(flow.compilation_hour) ||
        flow.compilation_hour < 0 ||
        flow.compilation_hour > 23
      )
        return "Час подготовки подборки: целое число от 0 до 23.";
    }
  }
  if (step == null || step === 3) {
    if (!Number.isSafeInteger(flow.schedule_horizon_hours) || flow.schedule_horizon_hours < 1 || flow.schedule_horizon_hours > 168)
      return "Горизонт календаря: целое число от 1 до 168 часов.";
    if (!Number.isSafeInteger(flow.max_source_age_hours) || flow.max_source_age_hours < 1 || flow.max_source_age_hours > 720)
      return "Возраст клипа на дату выпуска: целое число от 1 до 720 часов.";
    try {
      new Intl.DateTimeFormat("ru", { timeZone: flow.timezone }).format();
    } catch {
      return "Укажите существующий часовой пояс, например Asia/Tomsk.";
    }
    if (
      !Number.isSafeInteger(flow.daily_shorts) ||
      flow.daily_shorts < 1 ||
      flow.daily_shorts > 100 ||
      !Number.isSafeInteger(flow.daily_compilations) ||
      flow.daily_compilations < 1 ||
      flow.daily_compilations > 10
    )
      return "Лимит Shorts: 1–100; подборок: 1–10 в сутки.";
    for (const [enabled, slots] of [
      [flow.shorts_enabled, flow.shorts_slots],
      [flow.compilations_enabled, flow.compilation_slots],
    ] as const) {
      if (flow.auto_publish && enabled && !slots.length)
        return "В автоматическом режиме задайте время для каждого включённого формата.";
      if (slots.some((value) => !/^([01]\d|2[0-3]):[0-5]\d$/.test(value)))
        return "Время публикации задаётся в формате ЧЧ:ММ, например 09:00, 12:00.";
    }
  }
  return null;
}
