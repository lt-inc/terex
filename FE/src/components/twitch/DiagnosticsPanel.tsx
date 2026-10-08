"use client";
import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { fetchTwitchDiagnostics } from "@/lib/api";
import type { TwitchFlowDiagnostics } from "@/types/twitch";
import { durationLabel } from "@/lib/twitch-status";

const labels: Record<string, [string, string]> = {
  ready: ["Готовые исходники", "ready"], need_download: ["Нужно скачать", "discovered"],
  queued: ["В очереди / ожидают места", "queued"], downloading: ["Скачиваются", "downloading"],
  verifying: ["Передаются / проверяются", "verifying"], retry: ["Ожидают повтора", "retry_wait"],
  failed: ["Ошибки", "failed"], excluded: ["Исключены", "excluded"],
  language_mismatch: ["Другой язык", "language_mismatch"], language_unknown: ["Язык неизвестен", "language_unknown"],
  age_filtered: ["Не проходят по возрасту", "age_filtered"], already_used: ["Уже в подборках этого YouTube-канала", "used"],
  duration_filtered: ["Не проходят по длине", "duration_filtered"], expired: ["Файл удалён", "media_expired"],
  views_filtered: ["Мало просмотров", "views_filtered"], too_new: ["Слишком новые", "too_new"],
  source_mismatch: ["Другой канал / категория", "source_mismatch"],
};
const capacityReasons: Record<string,string> = { calendar_full: "Ближайшие места заняты готовыми выпусками или подготовкой", flow_paused: "Поток на паузе", storage_low: "На диске MinIO мало места; новые скачивания ожидают очистки", storage_status_unknown: "Нет свежих данных о свободном месте в MinIO", waiting_quota: "Дневной бюджет исчерпан", waiting_capacity: "Ждём свободного места в ближайшем календаре" };
export default function DiagnosticsPanel({ flowID, timezone = "Asia/Tomsk" }: { flowID: string; timezone?: string }) {
  const [data, setData] = useState<TwitchFlowDiagnostics | null>(null);
  const [error, setError] = useState("");
  const requestID = useRef(0);
  const load = useCallback(async () => {
    const attempt = ++requestID.current;
    try { const next = await fetchTwitchDiagnostics(flowID); if (attempt === requestID.current) {setData(next); setError("");} }
    catch (failure) {if (attempt === requestID.current) setError(failure instanceof Error ? failure.message : String(failure));}
  }, [flowID]);
  useEffect(() => {setData(null); void load(); const timer = setInterval(() => {if (document.visibilityState === "visible") void load();},10000); return () => {clearInterval(timer); requestID.current++;};},[load]);
  const stamp = (value?: string) => value && !value.startsWith("0001-") ? new Intl.DateTimeFormat("ru",{timeZone: timezone,dateStyle:"short",timeStyle:"short"}).format(new Date(value)) : "место пока не найдено";
  if (!data) return <p className="text-xs text-amber-200">{error ? `Диагностика недоступна: ${error}` : "Проверяем исходники и ближайшие места…"}</p>;
  const c = data.compilation;
  return <section className="rounded-xl border border-slate-700 bg-slate-900/40 p-4 text-sm">
    <div className="flex flex-wrap justify-between gap-2"><h2 className="font-semibold">Готовность потока</h2><button type="button" className="text-xs text-indigo-300" onClick={() => void load()}>Обновить</button></div>
    <p className="mt-1 text-xs text-slate-400">Расчёт на {stamp(data.generated_at)}. Обновляется каждые 10 секунд, пока страница открыта. Просмотр не расходует квоту.</p>
    {error && <p className="mt-2 text-amber-200">Последнее обновление не удалось; ниже предыдущие данные: {error}</p>}
    <p className="mt-3">Для ближайшей подборки: <strong>{c.selected_clips ?? 0} клипов · {durationLabel(c.ready_duration_sec)}</strong> из {durationLabel(c.minimum_duration_sec)}; минимум {c.minimum_clips} клипов.</p>
    <p className="mt-1 text-xs text-slate-400">Ближайшая дата: {stamp(c.scheduled_at)}. Учитываются целые клипы, секундные переходы, баланс и отсутствие повторов.</p>
    {c.missing_duration_sec > 0 && <p className="mt-2 text-amber-200">До готовности не хватает {durationLabel(c.missing_duration_sec)}. Уже готовятся {data.expected_clips} клипов, ожидаемая длина {durationLabel(data.expected_duration_sec)}; они войдут в готовый состав только после проверки. Дополнительно нужно ориентировочно {data.estimated_additional_clips} новых скачиваний по {Math.round(data.average_duration_sec)} сек{data.average_estimated ? " (оценка 30 сек: готовых исходников ещё нет)" : " (средняя длина подходящих готовых файлов)"}. Это оценка, а не обещание выпуска.</p>}
    {(data.messages || []).map((message,i)=><p key={i} className="mt-2 text-xs text-amber-200">{message}</p>)}
    <p className="mt-3 text-xs text-slate-400">Исходники для ближайшей даты. Найденные метаданные сами по себе не означают, что файл скачан; Short для подборки не требуется.</p>
    <div className="mt-2 flex flex-wrap gap-x-4 gap-y-2 text-xs">{Object.entries(labels).map(([key,[label,state]])=> <Link key={key} className="text-indigo-300 hover:underline" href={`/admin/twitch/clips?flow=${encodeURIComponent(flowID)}&format=compilation&state=${state}&show_filtered=true`}>{label}: {!data.collector.available && ["queued","downloading","retry"].includes(key) ? "неизвестно" : data.sources[key] || 0}</Link>)}</div>
    <div className="mt-4 border-t border-slate-700 pt-3">
      <p className="font-medium">Общий бюджет всех потоков</p>
      <p className="mt-1 text-xs">Зарезервировано / потрачено {data.collector.spent} из {data.collector.daily_limit}; осталось {data.collector.remaining}. Сутки: {data.collector.timezone}.</p><p className="mt-1 text-xs text-slate-400">Целевая доля подборок: {data.collector.compilation_budget_percent}%. {data.collector.admission_mode === "shadow" ? "Пока только расчёт: новый порядок допуска не влияет на скачивание." : "Скачивание направляется на ближайшие выпуски; доля не гарантирует равного распределения между потоками."} Один общий файл учитывается один раз.</p><div className="mt-2 flex flex-wrap gap-3 text-xs">{(data.collector.by_flow || []).map((flow,i)=><span key={i}>{flow.name} · {flow.format === "compilation" ? "подборки" : flow.format === "short" ? "Shorts" : "общие / прежние"}: {flow.spent}</span>)}</div>{!data.collector.available && <p className="mt-2 text-amber-200">{data.collector.message} Готовность проверенных файлов рассчитана main-be; неизвестные состояния очереди не означают отсутствие задач.</p>}
    </div>
    {data.collector.available && <div className="mt-4 border-t border-slate-700 pt-3"><p className="font-medium">Последняя проверка Twitch</p>{!data.collector.discovery?.length && <p className="mt-1 text-xs text-slate-400">Данных последней проверки пока нет: коллектор мог перезапуститься. Это не означает, что подходящих клипов нет.</p>}{data.collector.discovery?.map((report,i)=><p key={i} className="mt-2 text-xs">Источник {report.broadcaster_id || "категория"}, {stamp(report.checked_at)}: получено {report.fetched}, нужный язык {report.language_matched}, другой {report.language_rejected}, неизвестный {report.language_unknown}. {report.page_limit_reached && <span className="text-amber-200">Достигнут предел страниц Twitch: результат поиска неполный, остальные клипы пока не просмотрены.</span>}{report.error && <span className="text-red-300"> {report.error}</span>}</p>)}</div>}
    <div className="mt-4 border-t border-slate-700 pt-3"><p className="font-medium">Календарь YouTube-канала на {data.capacity.horizon_hours} ч</p>{(data.capacity.formats || []).map((format)=><p key={format.format} className="mt-1 text-xs">{format.format === "compilation" ? "Подборки" : "Shorts"}: {format.occupied_slots} запланировано · {format.preparing_slots} готовятся · {format.free_slots} свободно. {format.reason ? (capacityReasons[format.reason] || `Ожидание: ${format.reason}`) : ""}</p>)}<p className="mt-1 text-xs text-slate-400">Места и дневные лимиты общие для потоков, направленных на один YouTube-канал.</p>{data.competitors.map((flow,i)=><p key={i} className="mt-1 text-xs">{flow.name} · {flow.format === "compilation" ? "подборки" : "Shorts"}: {flow.scheduled} запланировано, {flow.preparing} готовятся; ближайшее {stamp(flow.nearest_at)}.</p>)}</div>
  </section>;
}
