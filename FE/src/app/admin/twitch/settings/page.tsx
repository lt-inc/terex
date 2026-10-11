"use client";
import { useCallback, useEffect, useState } from "react";
import Toggle from "@/components/ui/Toggle";
import TwitchNav from "@/components/twitch/TwitchNav";
import ResetPanel from "@/components/twitch/ResetPanel";
import {
  fetchTwitchSettings,
  fetchTwitchStatus,
  saveTwitchSettings,
} from "@/lib/api";
import type { TwitchSettings, TwitchCollectorStatus } from "@/types/twitch";
const input =
  "w-full rounded-lg border border-slate-600 bg-slate-700 px-3 py-2 text-sm";
export default function TwitchServiceSettings() {
  const [settings, setSettings] = useState<TwitchSettings | null>(null);
  const [status, setStatus] = useState<TwitchCollectorStatus | null>(null);
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [notice, setNotice] = useState("");
  const load = useCallback(async () => {
    try {
      const [value, current] = await Promise.all([
        fetchTwitchSettings(),
        fetchTwitchStatus(),
      ]);
      setSettings({ ...value, compilation_budget_percent: value.compilation_budget_percent ?? 70, admission_mode: value.admission_mode || "shadow" });
      setStatus(current);
      setError("");
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    const timer = setInterval(() => {
      if (document.visibilityState === "visible")
        void fetchTwitchStatus()
          .then(setStatus)
          .catch(() => {});
    }, 10000);
    return () => clearInterval(timer);
  }, []);
  async function save() {
    if (!settings) return;
    if (
      !Number.isSafeInteger(settings.daily_downloads) ||
      settings.daily_downloads < 1 ||
      settings.daily_downloads > 100 ||
      !Number.isSafeInteger(settings.per_channel_daily_downloads) ||
      settings.per_channel_daily_downloads < 1 ||
      settings.per_channel_daily_downloads > settings.daily_downloads ||
      !Number.isSafeInteger(settings.backlog_limit) ||
      settings.backlog_limit < 1 ||
      settings.backlog_limit > 1000 ||
      !Number.isInteger(settings.compilation_budget_percent) ||
      settings.compilation_budget_percent! < 0 || settings.compilation_budget_percent! > 100
    ) {
      setError(
        "Проверьте бюджет: 1–100 скачиваний, лимит стримера не выше общего, очередь 1–1000.",
      );
      return;
    }
    setBusy(true);
    setError("");
    setNotice("");
    try {
      setSettings(await saveTwitchSettings(settings));
      setNotice(
        "Общие настройки сохранены. Бюджет применяется ко всем потокам.",
      );
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="flex flex-col gap-5 p-4 sm:p-6">
      <h1 className="text-xl font-semibold">Настройки Twitch collector</h1>
      <TwitchNav />
      <p className="text-sm text-slate-400">
        Общий бюджет защищает очередь скачивания. Форматы, фильтры и расписание
        задаются внутри каждого потока.
      </p>
      {error && (
        <p role="alert" className="text-sm text-red-400">
          {error}
        </p>
      )}
      {notice && (
        <p role="status" className="text-sm text-emerald-400">
          {notice}
        </p>
      )}
      {status && (
        <section className="rounded-xl border border-slate-700 bg-slate-800 p-4 text-sm">
          <p>
            {status.credentials_ready
              ? "Ключи Twitch настроены"
              : "Ключи Twitch отсутствуют"}{" "}
            ·{" "}
            {status.running
              ? "Идёт проверка"
              : status.paused
                ? "Сбор остановлен"
                : "Collector ожидает проверки"}
          </p>
          <p className="mt-2 text-slate-400">
            Ждут назначения: {status.waiting_assignment ?? "—"} · разрешено скачивание: {status.queued} · скачивается: {status.downloading} ·
            ждут проверки: {status.verification_pending ?? "—"} · проверяется: {status.verifying ?? "—"} · исходников готово: {status.imported} · ошибок: {status.failed}
          </p>
          {status.block_reason && (
            <p className="mt-2 text-amber-300">{status.block_reason}</p>
          )}
          {status.last_error && (
            <p className="mt-2 text-red-300">{status.last_error}</p>
          )}
        </section>
      )}
      {settings ? (
        <form
          className="flex max-w-3xl flex-col gap-4 rounded-xl border border-slate-700 bg-slate-800 p-5"
          onSubmit={(event) => {
            event.preventDefault();
            void save();
          }}
        >
          <Toggle
            label="Разрешить работу коллектора"
            checked={settings.enabled}
            disabled={busy}
            onChange={(enabled) => setSettings({ ...settings, enabled })}
          />
          <p className="text-xs text-slate-400">
            Выключение останавливает новые проверки и скачивания всех потоков.
            Для отдельного потока используйте его переключатель.
          </p>
          <p className="text-xs text-slate-400">
            Общий бюджет скачиваний обновляется по календарным суткам в{" "}
            {settings.timezone}. Часовой пояс публикаций задаётся для каждого
            YouTube-канала отдельно.
          </p>
          <div className="grid gap-4 sm:grid-cols-2">
            {(
              [
                ["daily_downloads", "Всего новых скачиваний в сутки", 100],
                [
                  "per_channel_daily_downloads",
                  "Скачиваний одного стримера в сутки",
                  100,
                ],
                ["backlog_limit", "Максимум необработанных исходников", 1000],
              ] as const
            ).map(([field, label, max]) => (
              <label
                key={field}
                className="flex flex-col gap-1 text-sm text-slate-300"
              >
                {label}
                <input
                  className={input}
                  type="number"
                  min={1}
                  max={max}
                  required
                  value={settings[field]}
                  disabled={busy}
                  onChange={(event) =>
                    setSettings({
                      ...settings,
                      [field]: Number(event.target.value),
                    })
                  }
                />
              </label>
            ))}
          </div>
          <button
            disabled={busy}
            className="self-start rounded-lg bg-indigo-600 px-4 py-2 text-sm disabled:opacity-50"
          >
            {busy ? "Сохраняем…" : "Сохранить настройки"}
          </button>
          <label className="flex flex-col gap-1 text-sm">Целевая доля скачиваний для подборок, %<input type="number" className={input} min={0} max={100} value={settings.compilation_budget_percent ?? 70} onChange={event=>setSettings({...settings,compilation_budget_percent:Number(event.target.value)})}/><span className="text-xs text-slate-400">Общий бюджет распределяется по ближайшим выпускам. 70% — ориентир для исходников подборок; неиспользованный остаток доступен Shorts. Один файл для двух форматов расходует одно скачивание.</span></label>
          <label className="flex flex-col gap-1 text-sm">Режим допуска скачиваний<select className={input} value={settings.admission_mode || "shadow"} onChange={event=>setSettings({...settings,admission_mode:event.target.value as "shadow"|"enforce"})}><option value="shadow">Только расчёт и диагностика</option><option value="enforce">Применять план ближайших выпусков</option></select><span className="text-xs text-slate-400">Сначала проверьте диагностику в режиме расчёта. После включения файлы скачиваются для ближайших выпусков, а не просто по количеству найденных клипов.</span></label>
        </form>
      ) : (
        <p className="text-slate-400">Загружаем настройки…</p>
      )}
      <ResetPanel />
    </div>
  );
}
