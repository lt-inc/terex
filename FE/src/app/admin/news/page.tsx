"use client";
import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import NewsNav from "@/components/news/NewsNav";
import ConfirmAction from "@/components/ui/ConfirmAction";
import Toggle from "@/components/ui/Toggle";
import { deleteNewsFlow, fetchNewsFlows, fetchNewsStatus, generateNewsPost, updateNewsFlow } from "@/lib/news";
import { relativeTime } from "@/lib/news-flow";
import type { NewsFlow, NewsStatus } from "@/types/news";

const button = "rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600 disabled:opacity-40";
const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));

export default function NewsFlowsPage() {
  const [flows, setFlows] = useState<NewsFlow[]>([]);
  const [status, setStatus] = useState<NewsStatus | null>(null);
  const [remove, setRemove] = useState<NewsFlow | null>(null);
  const [busy, setBusy] = useState(0);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  const load = useCallback(async () => {
    const [f, s] = await Promise.allSettled([fetchNewsFlows(), fetchNewsStatus()]);
    if (f.status === "fulfilled") setFlows(f.value ?? []);
    if (s.status === "fulfilled") setStatus(s.value);
    setError([f, s].flatMap((r) => (r.status === "rejected" ? [msg(r.reason)] : [])).join(" · "));
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  async function run(id: number, work: () => Promise<string | void>) {
    setBusy(id);
    setError("");
    setNotice("");
    try {
      const done = await work();
      if (done) setNotice(done);
      await load();
    } catch (e) {
      setError(msg(e));
    } finally {
      setBusy(0);
    }
  }

  return (
    <div className="flex flex-col gap-5 p-4 sm:p-6">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">Новостные потоки</h1>
        <Link href="/admin/news/new" className="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white hover:bg-indigo-500">Новый поток</Link>
        <button type="button" className={button} onClick={() => void load()}>Обновить</button>
      </div>
      <NewsNav />
      {error && <p role="alert" className="text-sm text-red-400">{error}</p>}
      {notice && <p role="status" className="text-sm text-emerald-400">{notice}</p>}

      {status && (
        <section className="rounded-xl border border-slate-700 bg-slate-800 p-4 text-sm text-slate-300">
          <p>
            {status.llm_ready ? "LLM подключена" : <span className="text-red-400">Не задан OPENROUTER_API_KEY — генерация и кластеризация остановлены</span>}
            {" · "}Статей за сутки: {status.articles_24h}
            {" · "}Ждут эмбеддинга: {status.unembedded}
            {" · "}Черновиков на ревью: {status.pending_posts}
            {" · "}Последний цикл: {relativeTime(status.last_tick_at)}
          </p>
          {status.last_tick_error && <p className="mt-1 text-amber-300">Ошибка цикла: {status.last_tick_error}</p>}
        </section>
      )}

      {flows.length === 0 && <p className="text-sm text-slate-400">Потоков пока нет. Подключите канал и источники, затем создайте первый поток.</p>}
      <div className="grid gap-4 lg:grid-cols-2">
        {flows.map((f) => (
          <article key={f.id} className="flex flex-col gap-3 rounded-xl border border-slate-700 bg-slate-800 p-4">
            <div className="flex items-start gap-3">
              <div className="flex-1">
                <h2 className="font-medium">{f.name}</h2>
                <p className="text-xs text-slate-400">{f.topic || "Без темы — все новости из источников"}</p>
              </div>
              <div className="w-40 shrink-0">
                <Toggle label={f.enabled ? "Включён" : "Выключен"} checked={f.enabled} disabled={busy === f.id}
                  onChange={(v) => void run(f.id, async () => { await updateNewsFlow(f.id, { enabled: v }); })} />
              </div>
            </div>
            <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs text-slate-400">
              <dt>Канал</dt><dd className="text-slate-200">{f.channel_title || "— не выбран —"}</dd>
              <dt>Режим</dt><dd className="text-slate-200">{f.approve_mode === "auto" ? "Автоапрув" : "По апруву"}</dd>
              <dt>Язык</dt><dd className="text-slate-200">{f.language === "ru" ? "Русский" : "English"}</dd>
              <dt>Источников</dt><dd className="text-slate-200">{f.source_ids.length}</dd>
              <dt>Лимит</dt><dd className="text-slate-200">{f.posts_per_day}/день, {f.window_start_hour}–{f.window_end_hour} ч</dd>
              <dt>На ревью / сегодня</dt><dd className="text-slate-200">{f.pending_posts ?? 0} / {f.posted_today ?? 0}</dd>
              <dt>Сюжетов в очереди</dt><dd className="text-slate-200">{f.open_clusters ?? 0}</dd>
              <dt>Последний пост</dt><dd className="text-slate-200">{relativeTime(f.last_generated_at)}</dd>
            </dl>
            {f.last_error && <p className="text-xs text-amber-300">Ошибка: {f.last_error}</p>}
            <div className="flex flex-wrap gap-2">
              <Link href={`/admin/news/${f.id}`} className={button}>Настроить</Link>
              <Link href={`/admin/news/posts?flow=${f.id}`} className={button}>Посты</Link>
              <button type="button" className={button} disabled={busy === f.id}
                onClick={() => void run(f.id, async () => { const p = await generateNewsPost(f.id); return p ? "Черновик создан — смотрите «Посты»." : "Сейчас нечего публиковать."; })}>
                {busy === f.id ? "Работаем…" : "Создать пост"}
              </button>
              <button type="button" className="ml-auto rounded-lg bg-red-900/60 px-3 py-2 text-sm text-red-200 hover:bg-red-800 disabled:opacity-40" disabled={busy === f.id} onClick={() => setRemove(f)}>
                Удалить
              </button>
            </div>
          </article>
        ))}
      </div>
      {remove && (
        <ConfirmAction
          title="Удалить поток?"
          message={`«${remove.name}» будет удалён вместе с сюжетами и постами. Уже опубликованные сообщения в Telegram останутся.`}
          confirmLabel="Удалить"
          onCancel={() => setRemove(null)}
          onConfirm={() => {
            const target = remove;
            setRemove(null);
            void run(target.id, async () => { await deleteNewsFlow(target.id); return "Поток удалён."; });
          }}
        />
      )}
    </div>
  );
}
