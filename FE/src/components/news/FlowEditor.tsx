"use client";
import Link from "next/link";
import { useEffect, useState } from "react";
import Toggle from "@/components/ui/Toggle";
import { fetchNewsChannels, fetchNewsSources } from "@/lib/news";
import { DEFAULT_NEWS_FLOW, NEWS_TIMEZONES, validateNewsFlow } from "@/lib/news-flow";
import type { NewsChannel, NewsFlow, NewsSource } from "@/types/news";

const input = "w-full rounded-lg border border-slate-600 bg-slate-700 px-3 py-2 text-sm";
const card = "rounded-xl border border-slate-700 bg-slate-800 p-4";
const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));

type Draft = Omit<NewsFlow, "id"> & { id?: number };

export default function FlowEditor({ initial, onSave, saving }: { initial?: NewsFlow; onSave: (flow: Draft) => Promise<void>; saving: boolean }) {
  const [flow, setFlow] = useState<Draft>(initial ?? DEFAULT_NEWS_FLOW);
  const [sources, setSources] = useState<NewsSource[]>([]);
  const [channels, setChannels] = useState<NewsChannel[]>([]);
  const [error, setError] = useState("");
  const [loadError, setLoadError] = useState("");

  useEffect(() => {
    if (initial) setFlow(initial);
  }, [initial]);
  useEffect(() => {
    void Promise.allSettled([fetchNewsSources(), fetchNewsChannels()]).then(([s, c]) => {
      if (s.status === "fulfilled") setSources(s.value ?? []);
      if (c.status === "fulfilled") setChannels(c.value ?? []);
      const failed = [s, c].find((r) => r.status === "rejected");
      setLoadError(failed && failed.status === "rejected" ? msg(failed.reason) : "");
    });
  }, []);

  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => setFlow((f) => ({ ...f, [key]: value }));
  const num = (key: keyof Draft) => (e: React.ChangeEvent<HTMLInputElement>) => set(key, Number(e.target.value) as never);
  const toggleSource = (id: number) =>
    set("source_ids", flow.source_ids.includes(id) ? flow.source_ids.filter((x) => x !== id) : [...flow.source_ids, id]);

  async function submit() {
    const problem = validateNewsFlow(flow as NewsFlow);
    if (problem) {
      setError(problem);
      return;
    }
    setError("");
    try {
      await onSave(flow);
    } catch (e) {
      setError(msg(e));
    }
  }

  const channel = channels.find((c) => c.id === flow.channel_id);

  return (
    <div className="flex flex-col gap-4">
      {loadError && <p role="alert" className="text-sm text-red-400">Не удалось загрузить источники или каналы: {loadError}</p>}

      <section className={card}>
        <h2 className="mb-3 text-sm font-semibold">Тема</h2>
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="text-xs text-slate-400">Название потока
            <input className={input} value={flow.name} onChange={(e) => set("name", e.target.value)} placeholder="Крипто-новости" maxLength={120} />
          </label>
          <label className="text-xs text-slate-400">Язык постов
            <select className={input} value={flow.language} onChange={(e) => set("language", e.target.value as Draft["language"])}>
              <option value="ru">Русский</option>
              <option value="en">English</option>
            </select>
          </label>
          <label className="text-xs text-slate-400 sm:col-span-2">О чём канал
            <textarea className={input} rows={2} value={flow.topic} onChange={(e) => set("topic", e.target.value)} maxLength={500}
              placeholder="Криптовалюты и блокчейн: курсы, регулирование, крупные сделки" />
          </label>
        </div>
        <p className="mt-2 text-xs text-slate-400">Новости, далёкие от этой темы, отбрасываются. Оставьте поле пустым, чтобы брать всё из выбранных источников.</p>
      </section>

      <section className={card}>
        <h2 className="mb-3 text-sm font-semibold">Источники</h2>
        {sources.length === 0 ? (
          <p className="text-sm text-slate-400">Источников нет. <Link className="text-indigo-300 underline" href="/admin/news/sources">Добавьте источники</Link>.</p>
        ) : (
          <ul className="grid gap-2 sm:grid-cols-2">
            {sources.map((s) => (
              <li key={s.id}>
                <label className="flex cursor-pointer items-start gap-2 rounded-lg border border-slate-700 p-2 text-sm hover:bg-slate-700/40">
                  <input type="checkbox" className="mt-1" checked={flow.source_ids.includes(s.id)} onChange={() => toggleSource(s.id)} />
                  <span>
                    {s.name} <span className="text-xs text-slate-500">{s.kind === "rss" ? "RSS" : "Google News"} · {s.lang}</span>
                    {!s.enabled && <span className="ml-2 text-xs text-amber-300">выключен</span>}
                  </span>
                </label>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className={card}>
        <h2 className="mb-3 text-sm font-semibold">Публикация</h2>
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="text-xs text-slate-400">Telegram-канал
            <select className={input} value={flow.channel_id ?? ""} onChange={(e) => set("channel_id", e.target.value ? Number(e.target.value) : null)}>
              <option value="">— не выбран —</option>
              {channels.map((c) => <option key={c.id} value={c.id}>{c.title} ({c.chat_id}){c.status !== "ok" ? " — не подключён" : ""}</option>)}
            </select>
          </label>
          <label className="text-xs text-slate-400">Режим
            <select className={input} value={flow.approve_mode} onChange={(e) => set("approve_mode", e.target.value as Draft["approve_mode"])}>
              <option value="by_approve">По апруву — черновик ждёт вашего решения</option>
              <option value="auto">Автоапрув — публикуется сразу</option>
            </select>
          </label>
        </div>
        {channels.length === 0 && <p className="mt-2 text-xs text-slate-400"><Link className="text-indigo-300 underline" href="/admin/news/channels">Подключите Telegram-канал</Link>, чтобы выбрать его здесь.</p>}
        {channel && channel.status !== "ok" && <p className="mt-2 text-xs text-amber-300">Канал не проходит проверку: {channel.last_error || "ещё не проверен"}.</p>}
        {flow.approve_mode === "auto" && <p className="mt-2 text-xs text-amber-300">В автоматическом режиме посты уходят в канал без вашего просмотра. Начните с «По апруву» и переключайтесь, когда качество устроит.</p>}
        <div className="mt-3 grid gap-3 sm:grid-cols-4">
          <label className="text-xs text-slate-400">Постов в день
            <input className={input} type="number" min={1} max={48} value={flow.posts_per_day} onChange={num("posts_per_day")} />
          </label>
          <label className="text-xs text-slate-400">С часа
            <input className={input} type="number" min={0} max={23} value={flow.window_start_hour} onChange={num("window_start_hour")} />
          </label>
          <label className="text-xs text-slate-400">До часа
            <input className={input} type="number" min={1} max={24} value={flow.window_end_hour} onChange={num("window_end_hour")} />
          </label>
          <label className="text-xs text-slate-400">Часовой пояс
            <select className={input} value={flow.timezone} onChange={(e) => set("timezone", e.target.value)}>
              {[...new Set([flow.timezone, ...NEWS_TIMEZONES])].map((z) => <option key={z} value={z}>{z}</option>)}
            </select>
          </label>
          <label className="text-xs text-slate-400">Мин. интервал, мин
            <input className={input} type="number" min={0} max={1440} value={flow.min_interval_min} onChange={num("min_interval_min")} />
          </label>
          <label className="text-xs text-slate-400">Черновиков на ревью, макс.
            <input className={input} type="number" min={1} max={50} value={flow.max_pending} onChange={num("max_pending")} />
          </label>
        </div>
        <p className="mt-2 text-xs text-slate-400">Посты равномерно распределяются по активным часам: интервал не меньше заданного и не меньше ¾ от окна, делённого на число постов.</p>
      </section>

      <section className={card}>
        <h2 className="mb-3 text-sm font-semibold">Отбор сюжетов</h2>
        <div className="grid gap-3 sm:grid-cols-3">
          <label className="text-xs text-slate-400">Мин. изданий в сюжете
            <input className={input} type="number" min={1} max={20} value={flow.min_sources} onChange={num("min_sources")} />
          </label>
          <label className="text-xs text-slate-400">Релевантность теме (0–1)
            <input className={input} type="number" min={0} max={1} step={0.05} value={flow.min_relevance} onChange={num("min_relevance")} />
          </label>
          <label className="text-xs text-slate-400">Окно свежести, ч
            <input className={input} type="number" min={1} max={168} value={flow.window_hours} onChange={num("window_hours")} />
          </label>
        </div>
        <p className="mt-2 text-xs text-slate-400">
          Чем больше независимых изданий пишет об одном событии, тем выше сюжет в очереди. Если в канал попадает лишнее — поднимайте релевантность (обычно 0.25–0.45); если не хватает новостей — снижайте.
        </p>
      </section>

      <section className={card}>
        <h2 className="mb-3 text-sm font-semibold">Генерация</h2>
        <div className="grid gap-3 sm:grid-cols-3">
          <label className="text-xs text-slate-400">Длина поста, символов
            <input className={input} type="number" min={200} max={3500} value={flow.max_chars} onChange={num("max_chars")} />
          </label>
          <label className="text-xs text-slate-400 sm:col-span-2">Модель OpenRouter (необязательно)
            <input className={input} value={flow.model} onChange={(e) => set("model", e.target.value.trim())} placeholder="по умолчанию из настроек сервиса" />
          </label>
          <label className="text-xs text-slate-400 sm:col-span-3">Стиль и пожелания
            <textarea className={input} rows={3} value={flow.style_prompt} onChange={(e) => set("style_prompt", e.target.value)} maxLength={2000}
              placeholder="Коротко и по делу, без восклицательных знаков. Цифры выделять. В конце — одна фраза «что это значит»." />
          </label>
        </div>
        <div className="mt-3 max-w-sm"><Toggle label="Добавлять ссылки на источники" checked={flow.include_link} onChange={(v) => set("include_link", v)} /></div>
      </section>

      <section className={`${card} flex flex-wrap items-center gap-4`}>
        <div className="max-w-xs flex-1"><Toggle label="Поток включён" checked={flow.enabled} onChange={(v) => set("enabled", v)} /></div>
        {error && <p role="alert" className="text-sm text-red-400">{error}</p>}
        <button type="button" className="ml-auto rounded-lg bg-indigo-600 px-5 py-2 text-sm text-white hover:bg-indigo-500 disabled:opacity-40" disabled={saving} onClick={() => void submit()}>
          {saving ? "Сохраняем…" : "Сохранить"}
        </button>
      </section>
    </div>
  );
}
