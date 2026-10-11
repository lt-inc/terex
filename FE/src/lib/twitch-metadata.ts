import type { TwitchMetadataSettings } from "@/types/twitch";

export const metadataVariables = ["title", "streamer", "streamers", "category", "clip_count"] as const;

export const standardMetadataSettings = (): TwitchMetadataSettings => ({
  short_intro_template: "",
  compilation_intro_template: "",
  hashtag_mode: "standard",
  custom_hashtags: [],
});

export function parseMetadataHashtags(value: string): string[] {
  return value.split(/[\s,;]+/).map((tag) => tag.replace(/^#+/, "")).filter(Boolean);
}

export function validateMetadataSettings(settings?: TwitchMetadataSettings): string | null {
  if (!settings) return null;
  if (settings.hashtag_mode && !["standard", "custom"].includes(settings.hashtag_mode))
    return "Выберите стандартные или собственные хештеги.";
  for (const template of [settings.short_intro_template, settings.compilation_intro_template]) {
    if (!template) continue;
    for (const match of template.matchAll(/\{([^{}]*)\}/g)) {
      if (!(metadataVariables as readonly string[]).includes(match[1]))
        return `Неизвестная переменная {${match[1]}}. Разрешены: ${metadataVariables.map((name) => `{${name}}`).join(", ")}.`;
    }
    if (/[{}]/.test(template.replace(/\{[^{}]*\}/g, "")))
      return "Проверьте фигурные скобки в шаблоне описания.";
  }
  return null;
}
