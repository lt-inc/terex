"use client";
import { useCallback, useEffect, useState } from "react";
import { fetchTwitchCapacity } from "@/lib/api";
import type { TwitchCapacity } from "@/types/twitch";

export default function CapacityPanel({ flowID, timezone }: { flowID: string; timezone: string }) {
  const [capacity, setCapacity] = useState<TwitchCapacity | null>(null);
  const [error, setError] = useState("");
  const load = useCallback(async () => {
    try { setCapacity(await fetchTwitchCapacity(flowID)); setError(""); }
    catch (failure) { setError(failure instanceof Error ? failure.message : String(failure)); }
  }, [flowID]);
  useEffect(() => {
    void load();
    const timer = setInterval(() => { if (document.visibilityState === "visible") void load(); }, 30000);
    return () => clearInterval(timer);
  }, [load]);
  const stamp = (value?: string) => value ? new Intl.DateTimeFormat("ru", { timeZone: timezone, dateStyle: "short", timeStyle: "short" }).format(new Date(value)) : "—";
  if (error) return <p className="text-xs text-amber-200">Ёмкость календаря недоступна: {error}</p>;
  if (!capacity) return <p className="text-xs text-slate-500">Рассчитываем ближайшие места…</p>;
  return <section className="rounded-lg border border-slate-700 bg-slate-900/40 p-3 text-xs">
    <p className="font-medium text-slate-200">Подготовка на {capacity.horizon_hours} ч · до {stamp(capacity.horizon_end)} ({timezone})</p>
    {capacity.mode === "preview" && <p className="mt-1 text-amber-200">Пока только расчёт: ограничения включаются после проверки существующей очереди.</p>}
    <p className="mt-1 text-slate-400">Ёмкость общая для потоков этого YouTube-канала. Подготовка занимает место до публикации.</p>
    {(capacity.formats || []).map((format) => <div key={format.format} className="mt-2 border-t border-slate-700 pt-2">
      <p>{format.format === "compilation" ? "Подборки" : "Shorts"}: {format.occupied_slots} запланировано · {format.preparing_slots} в подготовке · <span className={format.free_slots ? "text-emerald-300" : "text-amber-200"}>{format.free_slots} свободно из {format.total_slots}</span></p>
      {format.next_slot_at && <p className="mt-1 text-slate-400">Ближайшее место: {stamp(format.next_slot_at)}</p>}
      {format.reason && <p className="mt-1 text-amber-200">{format.reason === "calendar_full" ? (capacity.mode === "preview" ? "Все ближайшие места заняты. После применения ограничений новый запас скачиваться не будет." : format.preparing_slots > 0 && format.format === "compilation" && (format.missing_clips || format.missing_duration_sec) ? "Все места заняты; уже начатую подборку можно дополнить в пределах её резерва." : `Календарь заполнен до ${stamp(capacity.horizon_end)}; новый запас не скачивается.`) : format.reason === "flow_paused" ? "Поток на паузе. Подготовка и новые публикации остановлены." : format.reason === "storage_low" ? "На диске MinIO недостаточно места. Новые скачивания и рендеры ждут; подробности в разделе «Хранение»." : format.reason === "storage_status_unknown" ? "Нет свежих данных о свободном месте. Новая подготовка ждёт проверки хранилища; начатые публикации продолжаются." : format.reason}</p>}
      {!!format.missing_clips && <p className="mt-1 text-slate-400">Для подборки не хватает {format.missing_clips} клипов.</p>}
      {!!format.missing_duration_sec && <p className="mt-1 text-slate-400">Для минимальной длины не хватает {Math.ceil(format.missing_duration_sec)} сек.</p>}
      {format.next_check_at && <p className="mt-1 text-slate-500">Следующий пересчёт: {stamp(format.next_check_at)}</p>}
    </div>)}
  </section>;
}
