import type { TwitchClipGrowth } from "@/types/twitch";

export default function GrowthBadge({ growth, pollMinutes = 144 }: { growth?: TwitchClipGrowth; pollMinutes?: number }) {
  const age = growth?.observed_at ? Date.now() - new Date(growth.observed_at).getTime() : Infinity;
  const stale = growth?.state === "stale" || age > Math.min(360, Math.max(30, 2 * pollMinutes)) * 60000;
  if (!growth || growth.state === "warming") return <p className="mt-1 text-xs text-slate-400">Рост пока неизвестен: нужны два наблюдения с интервалом от 15 минут. До этого рейтинг использует просмотры.</p>;
  if (growth.state === "counter_reset") return <p className="mt-1 text-xs text-amber-200">Twitch уменьшил счётчик просмотров. Набираем новую историю; прежняя скорость больше не используется.</p>;
  if (stale) return <p className="mt-1 text-xs text-amber-200">Данные о росте устарели. До свежего наблюдения клип оценивается по просмотрам.</p>;
  return <p className="mt-1 text-xs text-emerald-300" title="Скорость рассчитана по реальным наблюдениям. Метка быстрого роста сравнивает клип с другими измеренными клипами этого же стримера и не отменяет фильтры потока.">
    +{(growth.delta_views ?? 0).toLocaleString("ru")} за {Math.round((growth.interval_sec ?? 0) / 60)} мин · {Math.round(growth.views_per_hour ?? 0).toLocaleString("ru")} просмотров/ч
    {growth.anomalous && <span className="ml-2 rounded bg-emerald-900 px-2 py-0.5">Быстрый рост</span>}
  </p>;
}
