"use client";
import { useEffect, useState } from "react";
import { fetchAccountsList, fetchBackgrounds, fetchBanners } from "@/lib/api";
import { DEFAULT_YT_CONFIG, validateSlots } from "@/lib/youtube";
import type { AccountRecord } from "@/types";
import type { YTFlowConfig } from "@/types/youtube";

const input = "w-full rounded-lg border border-slate-600 bg-slate-700 px-3 py-2 text-sm";
const card = "rounded-xl border border-slate-700 bg-slate-800 p-4";
const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));

export interface FlowDraft {
  name: string;
  account_id: number;
  config: YTFlowConfig;
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <label className="flex flex-col gap-1 text-sm text-slate-300">
      <span>{label}</span>
      {children}
      {hint && <span className="text-xs text-slate-500">{hint}</span>}
    </label>
  );
}

export default function FlowForm({ initial, onSave, saving }: { initial?: FlowDraft; onSave: (draft: FlowDraft) => Promise<void>; saving: boolean }) {
  const [draft, setDraft] = useState<FlowDraft>(initial ?? { name: "", account_id: 0, config: DEFAULT_YT_CONFIG });
  const [slots, setSlots] = useState((initial?.config.slots ?? DEFAULT_YT_CONFIG.slots).join(" "));
  const [accounts, setAccounts] = useState<AccountRecord[]>([]);
  const [backgrounds, setBackgrounds] = useState<string[]>([]);
  const [banners, setBanners] = useState<string[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    if (initial) {
      setDraft(initial);
      setSlots(initial.config.slots.join(" "));
    }
  }, [initial]);
  useEffect(() => {
    void fetchAccountsList()
      .then((list) => setAccounts((list ?? []).filter((a) => a.platform === "youtube")))
      .catch((e) => setError(msg(e)));
  }, []);
  useEffect(() => {
    void Promise.all([fetchBackgrounds(), fetchBanners()])
      .then(([bg, bn]) => {
        setBackgrounds(bg ?? []);
        setBanners(bn ?? []);
      })
      .catch((e) => setError(msg(e)));
  }, []);

  const cfg = draft.config;
  const setCfg = <K extends keyof YTFlowConfig>(key: K, value: YTFlowConfig[K]) => setDraft((d) => ({ ...d, config: { ...d.config, [key]: value } }));
  const num = (key: keyof YTFlowConfig) => (e: React.ChangeEvent<HTMLInputElement>) => setCfg(key, Number(e.target.value) as never);

  async function submit() {
    const parsed = validateSlots(slots);
    if (!draft.name.trim()) return setError("Укажите название потока.");
    if (!draft.account_id) return setError("Выберите YouTube-канал, на который заливаем.");
    if (parsed.error) return setError(parsed.error);
    setError("");
    try {
      await onSave({ ...draft, config: { ...cfg, slots: parsed.slots } });
    } catch (e) {
      setError(msg(e));
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <section className={`${card} grid gap-4 sm:grid-cols-2`}>
        <Field label="Название">
          <input className={input} value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} maxLength={80} />
        </Field>
        <Field label="Заливаем на канал" hint="Лимит публикаций в день общий с Twitch-потоками этого канала.">
          <select className={input} value={draft.account_id} onChange={(e) => setDraft({ ...draft, account_id: Number(e.target.value) })}>
            <option value={0}>— выберите —</option>
            {accounts.map((a) => (
              <option key={a.id} value={a.id} disabled={a.connection_state !== "connected"}>
                {a.name}{a.connection_state !== "connected" ? " (не подключён)" : ""}
              </option>
            ))}
          </select>
        </Field>
      </section>

      <section className={`${card} grid gap-4 sm:grid-cols-3`}>
        <h2 className="font-medium sm:col-span-3">Что брать</h2>
        <label className="flex items-start gap-3 text-sm text-slate-300 sm:col-span-3">
          <input type="checkbox" className="mt-1" checked={cfg.all_time} onChange={(e) => setCfg("all_time", e.target.checked)} />
          <span>
            Весь канал целиком, а не только последние часы
            <span className="block text-xs text-slate-500">
              Берутся лучшие Shorts за всю историю канала по общим просмотрам. Историю читаем один раз (после включения нажмите «Проверить сейчас»), дальше ловим только новые видео.
              Глубина обхода стоит квоты: 200 видео — около 8 единиц, 1000 — около 40.
            </span>
          </span>
        </label>
        {cfg.all_time ? (
          <Field label="Глубина обхода, видео на канал" hint="Не больше 1000, новейшие Shorts первыми."><input type="number" min={1} max={1000} className={input} value={cfg.max_videos} onChange={num("max_videos")} /></Field>
        ) : (
          <Field label="Окно, часов" hint="Только видео не старше этого."><input type="number" min={1} className={input} value={cfg.window_hours} onChange={num("window_hours")} /></Field>
        )}
        <Field label="Минимум просмотров"><input type="number" min={1} className={input} value={cfg.min_views} onChange={num("min_views")} /></Field>
        {!cfg.all_time && <Field label="Минимум просмотров в час" hint="0 — не учитывать."><input type="number" min={0} className={input} value={cfg.min_views_per_hour} onChange={num("min_views_per_hour")} /></Field>}
        <Field label="Длительность от, сек"><input type="number" min={1} className={input} value={cfg.min_duration_sec} onChange={num("min_duration_sec")} /></Field>
        <Field label="Длительность до, сек" hint="Не больше 180."><input type="number" min={1} max={180} className={input} value={cfg.max_duration_sec} onChange={num("max_duration_sec")} /></Field>
      </section>

      <section className={`${card} grid gap-4 sm:grid-cols-3`}>
        <h2 className="font-medium sm:col-span-3">Публикация и бюджеты</h2>
        <Field label="Окна публикации" hint="Местное время канала, через пробел.">
          <input className={input} value={slots} onChange={(e) => setSlots(e.target.value)} />
        </Field>
        <Field label="Лимит потока в день" hint="0 — только лимит канала."><input type="number" min={0} className={input} value={cfg.daily_limit} onChange={num("daily_limit")} /></Field>
        <Field label="Скачиваний в день" hint="0 — без ограничения."><input type="number" min={0} className={input} value={cfg.daily_downloads} onChange={num("daily_downloads")} /></Field>
        <Field label="Режим">
          <select className={input} value={cfg.approve_mode} onChange={(e) => setCfg("approve_mode", e.target.value as YTFlowConfig["approve_mode"])}>
            <option value="by_approve">По апруву (страница Clips)</option>
            <option value="auto_approve">Автоматически</option>
          </select>
        </Field>
        <Field label="Бюджет квоты API, единиц/сутки" hint="Жёсткий лимит потока внутри общего пула.">
          <input type="number" min={1} className={input} value={cfg.quota_daily} onChange={num("quota_daily")} />
        </Field>
        <Field label="Обновлять просмотры, часов"><input type="number" min={1} className={input} value={cfg.refresh_hours} onChange={num("refresh_hours")} /></Field>
        <Field label="Полный обход источника, часов"><input type="number" min={1} className={input} value={cfg.scan_hours} onChange={num("scan_hours")} /></Field>
      </section>

      <section className={`${card} grid gap-4 sm:grid-cols-2`}>
        <label className="flex items-start gap-3 text-sm text-slate-300 sm:col-span-2">
          <input type="checkbox" className="mt-1" checked={cfg.brand_style} onChange={(e) => {
              const on = e.target.checked;
              setDraft((d) => ({ ...d, config: { ...d.config, brand_style: on, ...(on ? {} : { brand_background: "", brand_banner: "" }) } }));
            }} />
          <span>
            Добавить фирменный стиль
            <span className="block text-xs text-slate-500">
              Видео уменьшается до 75% и прижимается к верху по центру, под ним на фоне идут субтитры RU + EN по одному слову (жёлтым, то слово, что звучит сейчас),
              тон оригинального звука сдвигается на +2 полутона (против претензий Content ID). Нужны распознавание речи и перевод, поэтому видео обрабатывается дольше.
            </span>
          </span>
        </label>
        {cfg.brand_style && (
          <>
            <Field label="Фон" hint="Блюр — размытая копия самого видео. Остальное — из загруженных на странице Backgrounds.">
              <select className={input} value={cfg.brand_background} onChange={(e) => setCfg("brand_background", e.target.value)}>
                <option value="">Блюр</option>
                {backgrounds.map((n) => (
                  <option key={n} value={n}>{n}</option>
                ))}
              </select>
            </Field>
            <Field label="Баннер" hint="Внизу готового видео. Из загруженных на странице Banners.">
              <select className={input} value={cfg.brand_banner} onChange={(e) => setCfg("brand_banner", e.target.value)}>
                <option value="">Без баннера</option>
                {banners.map((n) => (
                  <option key={n} value={n}>{n}</option>
                ))}
              </select>
            </Field>
          </>
        )}
      </section>

      {error && <p role="alert" className="text-sm text-red-400">{error}</p>}
      <div>
        <button type="button" disabled={saving} onClick={() => void submit()} className="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white hover:bg-indigo-500 disabled:opacity-40">
          {saving ? "Сохраняем…" : "Сохранить"}
        </button>
      </div>
    </div>
  );
}
