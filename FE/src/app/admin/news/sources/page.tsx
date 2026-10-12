"use client";
import { useCallback, useEffect, useState } from "react";
import NewsNav from "@/components/news/NewsNav";
import Toggle from "@/components/ui/Toggle";
import ConfirmAction from "@/components/ui/ConfirmAction";
import {
  createNewsSource,
  deleteNewsSource,
  fetchNewsSourceNow,
  fetchNewsSources,
  testNewsSource,
  updateNewsSource,
} from "@/lib/news";
import { relativeTime } from "@/lib/news-flow";
import type { NewsFeedPreview, NewsSource } from "@/types/news";

const input = "w-full rounded-lg border border-slate-600 bg-slate-700 px-3 py-2 text-sm";
const button = "rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600 disabled:opacity-40";
const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));

type Draft = { kind: "rss" | "gnews"; name: string; url: string; query: string; lang: "ru" | "en"; fetch_interval_min: number };
const EMPTY: Draft = { kind: "rss", name: "", url: "", query: "", lang: "ru", fetch_interval_min: 30 };

export default function NewsSourcesPage() {
  const [sources, setSources] = useState<NewsSource[]>([]);
  const [draft, setDraft] = useState<Draft>(EMPTY);
  const [preview, setPreview] = useState<NewsFeedPreview | null>(null);
  const [remove, setRemove] = useState<NewsSource | null>(null);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  const load = useCallback(async () => {
    try {
      setSources((await fetchNewsSources()) ?? []);
      setError("");
    } catch (e) {
      setError(msg(e));
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  async function run(key: string, work: () => Promise<string | void>) {
    setBusy(key);
    setError("");
    setNotice("");
    try {
      const done = await work();
      if (done) setNotice(done);
      await load();
    } catch (e) {
      setError(msg(e));
    } finally {
      setBusy("");
    }
  }

  const ready = draft.name.trim() !== "" && (draft.kind === "rss" ? draft.url.trim() !== "" : draft.query.trim() !== "");

  return (
    <div className="flex flex-col gap-5 p-4 sm:p-6">
      <h1 className="text-xl font-semibold">Источники новостей</h1>
      <NewsNav />
      {error && <p role="alert" className="text-sm text-red-400">{error}</p>}
      {notice && <p role="status" className="text-sm text-emerald-400">{notice}</p>}

      <section className="rounded-xl border border-slate-700 bg-slate-800 p-4">
        <h2 className="mb-3 text-sm font-semibold">Добавить источник</h2>
        <div className="grid gap-3 sm:grid-cols-4">
          <label className="text-xs text-slate-400">Тип
            <select className={input} value={draft.kind} onChange={(e) => { setDraft({ ...draft, kind: e.target.value as Draft["kind"] }); setPreview(null); }}>
              <option value="rss">RSS / Atom лента</option>
              <option value="gnews">Поиск Google News по теме</option>
            </select>
          </label>
          <label className="text-xs text-slate-400">Название
            <input className={input} value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} placeholder="РБК" maxLength={120} />
          </label>
          {draft.kind === "rss" ? (
            <label className="text-xs text-slate-400 sm:col-span-2">Адрес ленты
              <input className={input} value={draft.url} onChange={(e) => setDraft({ ...draft, url: e.target.value })} placeholder="https://example.com/rss" />
            </label>
          ) : (
            <label className="text-xs text-slate-400 sm:col-span-2">Поисковый запрос
              <input className={input} value={draft.query} onChange={(e) => setDraft({ ...draft, query: e.target.value })} placeholder="биткоин OR ethereum" maxLength={200} />
            </label>
          )}
          <label className="text-xs text-slate-400">Язык источника
            <select className={input} value={draft.lang} onChange={(e) => setDraft({ ...draft, lang: e.target.value as Draft["lang"] })}>
              <option value="ru">Русский</option>
              <option value="en">English</option>
            </select>
          </label>
          <label className="text-xs text-slate-400">Обновлять, мин
            <input className={input} type="number" min={5} max={1440} value={draft.fetch_interval_min} onChange={(e) => setDraft({ ...draft, fetch_interval_min: Number(e.target.value) })} />
          </label>
        </div>
        {draft.kind === "gnews" && (
          <p className="mt-2 text-xs text-slate-400">
            Google News находит публикации разных изданий по запросу. Для полного текста лучше добавлять и прямые RSS-ленты: если ссылку не удаётся раскрыть, у статьи остаётся только заголовок.
          </p>
        )}
        <div className="mt-3 flex flex-wrap gap-2">
          <button type="button" className={button} disabled={busy !== "" || !ready} onClick={() => void run("test", async () => { setPreview(await testNewsSource(draft)); })}>
            {busy === "test" ? "Проверяем…" : "Проверить ленту"}
          </button>
          <button
            type="button"
            className="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white hover:bg-indigo-500 disabled:opacity-40"
            disabled={busy !== "" || !ready}
            onClick={() => void run("create", async () => { await createNewsSource({ ...draft, enabled: true }); setDraft(EMPTY); setPreview(null); return "Источник добавлен."; })}
          >
            Добавить
          </button>
        </div>
        {preview && (
          <div className="mt-3 rounded-lg border border-slate-700 bg-slate-900/50 p-3 text-sm">
            <p className="mb-2 text-slate-300">Лента доступна{preview.title ? `: ${preview.title}` : ""}. Последние записи:</p>
            <ul className="list-disc space-y-1 pl-5 text-xs text-slate-400">
              {preview.items.length === 0 && <li>В ленте пока нет записей.</li>}
              {preview.items.map((it) => <li key={it.link}>{it.title}</li>)}
            </ul>
          </div>
        )}
      </section>

      <section className="overflow-x-auto rounded-xl border border-slate-700">
        <table className="w-full text-left text-sm">
          <thead className="bg-slate-800 text-xs uppercase tracking-wide text-slate-400">
            <tr><th className="p-3">Источник</th><th className="p-3">Тип</th><th className="p-3">Обновлён</th><th className="p-3">Статей за 7 дн</th><th className="p-3">Включён</th><th className="p-3" /></tr>
          </thead>
          <tbody>
            {sources.length === 0 && <tr><td colSpan={6} className="p-4 text-slate-400">Источников пока нет.</td></tr>}
            {sources.map((s) => (
              <tr key={s.id} className="border-t border-slate-700 align-top">
                <td className="p-3">
                  <div className="font-medium">{s.name} <span className="text-xs text-slate-500">{s.lang}</span></div>
                  <div className="max-w-md truncate text-xs text-slate-400">{s.kind === "rss" ? s.url : `«${s.query}»`}</div>
                  {s.last_error && <div className="mt-1 text-xs text-red-400">{s.last_error}</div>}
                </td>
                <td className="p-3 text-slate-300">{s.kind === "rss" ? "RSS" : "Google News"}</td>
                <td className="p-3 text-slate-300">{relativeTime(s.last_fetched_at)}</td>
                <td className="p-3 text-slate-300">{s.articles}</td>
                <td className="p-3">
                  <Toggle label="" checked={s.enabled} disabled={busy !== ""} onChange={(v) => void run(`e${s.id}`, async () => { await updateNewsSource(s.id, { enabled: v }); })} />
                </td>
                <td className="p-3">
                  <div className="flex gap-2">
                    <button type="button" className={button} disabled={busy !== ""} onClick={() => void run(`f${s.id}`, async () => { const r = await fetchNewsSourceNow(s.id); return `Загружено новых статей: ${r.new_articles}.`; })}>
                      {busy === `f${s.id}` ? "Загрузка…" : "Загрузить"}
                    </button>
                    <button type="button" className="rounded-lg bg-red-900/60 px-3 py-2 text-sm text-red-200 hover:bg-red-800 disabled:opacity-40" disabled={busy !== ""} onClick={() => setRemove(s)}>
                      Удалить
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
      {remove && (
        <ConfirmAction
          title="Удалить источник?"
          message={`«${remove.name}» исчезнет из всех потоков вместе с собранными статьями. Уже созданные посты останутся.`}
          confirmLabel="Удалить"
          onCancel={() => setRemove(null)}
          onConfirm={() => {
            const target = remove;
            setRemove(null);
            void run(`d${target.id}`, async () => { await deleteNewsSource(target.id); return "Источник удалён."; });
          }}
        />
      )}
    </div>
  );
}
