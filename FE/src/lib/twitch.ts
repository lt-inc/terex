import type {
  CompilationItem,
  ImportedClip,
  TwitchSettings,
} from "@/types/twitch";

export const DEFAULT_TWITCH_SETTINGS: TwitchSettings = {
  enabled: false,
  target_account_id: 0,
  poll_interval_minutes: 144,
  window_hours: 72,
  first: 100,
  min_views: 100,
  min_age_minutes: 60,
  min_duration_sec: 5,
  max_duration_sec: 60,
  daily_downloads: 0,
  per_channel_daily_downloads: 2,
  daily_shorts: 5,
  daily_compilations: 1,
  compilation_min_clips: 3,
  compilation_max_clips: 10,
  compilation_hour: 21,
  backlog_limit: 40,
  timezone: "Asia/Tomsk",
};

export function validateTwitchSettings(
  settings: TwitchSettings,
): string | null {
  const ranges: [keyof TwitchSettings, number, number][] = [
    ["target_account_id", 0, Number.MAX_SAFE_INTEGER],
    ["poll_interval_minutes", 1, 10080],
    ["window_hours", 1, 720],
    ["first", 1, 100],
    ["min_views", 0, Number.MAX_SAFE_INTEGER],
    ["min_age_minutes", 0, Number.MAX_SAFE_INTEGER],
    ["daily_downloads", 0, 100],
    ["per_channel_daily_downloads", 1, 100],
    ["daily_shorts", 1, 100],
    ["daily_compilations", 1, 10],
    ["compilation_min_clips", 3, 10],
    ["compilation_max_clips", 3, 10],
    ["compilation_hour", 0, 23],
    ["backlog_limit", 1, 1000],
  ];
  for (const [field, minimum, maximum] of ranges) {
    const value = settings[field];
    if (
      typeof value !== "number" ||
      !Number.isSafeInteger(value) ||
      value < minimum ||
      value > maximum
    ) {
      return `${field.replaceAll("_", " ")} must be a whole number between ${minimum} and ${maximum}.`;
    }
  }
  if (settings.min_age_minutes >= settings.window_hours * 60)
    return "Minimum age must be shorter than the discovery window.";
  if (
    !Number.isFinite(settings.min_duration_sec) ||
    !Number.isFinite(settings.max_duration_sec) ||
    settings.min_duration_sec <= 0 ||
    settings.max_duration_sec < settings.min_duration_sec ||
    settings.max_duration_sec > 180
  ) {
    return "Clip durations must be greater than zero and no longer than 180 seconds; maximum must cover minimum.";
  }
  if (settings.daily_downloads > 0 && settings.per_channel_daily_downloads > settings.daily_downloads)
    return "The per-channel download limit must not exceed the total daily limit.";
  if (settings.compilation_min_clips > settings.compilation_max_clips)
    return "Maximum compilation size must cover the minimum.";
  try {
    new Intl.DateTimeFormat("en", { timeZone: settings.timezone }).format();
  } catch {
    return "Enter a valid timezone.";
  }
  return null;
}

export function eligibleImports(imports: ImportedClip[]): ImportedClip[] {
  return imports.filter(
    (item) =>
      !item.rejected &&
      !item.excluded &&
      (item.media_state === "ready" ||
        (item.state === "ready" &&
          !!item.clip_id &&
          Number.isSafeInteger(item.revision_id) &&
          (item.revision_id ?? 0) > 0 &&
          !item.error)),
  );
}

export function validateCompilation(
  title: string,
  items: CompilationItem[],
  minimum = 3,
  maximum = 10,
): string | null {
  if (!title.trim() || title.length > 100)
    return "Название должно содержать 1–100 символов.";
  if (items.length < minimum || items.length > maximum)
    return `Выберите ${minimum}–${maximum} разных клипов.`;
  if (
    new Set(items.map((item) => item.source_id || item.clip_id)).size !==
    items.length
  )
    return "Один клип может появиться в подборке только один раз.";
  if (
    items.some(
      (item) =>
        (!item.source_id &&
          (!item.clip_id ||
            !Number.isSafeInteger(item.revision_id) ||
            item.revision_id <= 0)) ||
        !Number.isFinite(item.start_sec) ||
        !Number.isFinite(item.end_sec) ||
        item.start_sec < 0 ||
        item.end_sec <= item.start_sec,
    )
  ) {
    return "Проверьте исходники и границы: начало не меньше нуля, конец позже начала; ссылка на существующую версию или исходник обязательна.";
  }
  return null;
}

export function moveCompilationItem(
  items: CompilationItem[],
  index: number,
  direction: -1 | 1,
): CompilationItem[] {
  const next = index + direction;
  if (index < 0 || index >= items.length || next < 0 || next >= items.length)
    return items;
  const reordered = [...items];
  [reordered[index], reordered[next]] = [reordered[next], reordered[index]];
  return reordered;
}
