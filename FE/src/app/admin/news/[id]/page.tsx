"use client";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import FlowEditor from "@/components/news/FlowEditor";
import NewsNav from "@/components/news/NewsNav";
import { fetchNewsFlow, fetchNewsStatus, fetchNewsStories, generateNewsPost, updateNewsFlow } from "@/lib/news";
import { flowBlockers, relativeTime } from "@/lib/news-flow";
import type { NewsFlow, NewsStory } from "@/types/news";

const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));

export default function EditNewsFlowPage() {
  const params = useParams<{ id: string }>();
  const id = Number(params.id);
  const [flow, setFlow] = useState<NewsFlow | undefined>();
  const [stories, setStories] = useState<NewsStory[]>([]);
  const [blockers, setBlockers] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [generating, setGenerating] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  const load = useCallback(async () => {
    try {
      const [f, st, s] = await Promise.all([fetchNewsFlow(id), fetchNewsStories(id), fetchNewsStatus()]);
      setFlow(f);
      setStories(st ?? []);
      setBlockers(flowBlockers(f, s.llm_ready));
      setError("");
    } catch (e) {
      setError(msg(e));
    }
  }, [id]);
  useEffect(() => {
    if (Number.isSafeInteger(id) && id > 0) void load();
  }, [id, load]);

  if (!Number.isSafeInteger(id) || id <= 0) return <p className="p-6 text-red-400">Некорректный номер потока.</p>;

  return (
    <div className="flex flex-col gap-5 p-4 sm:p-6">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">Поток: {flow?.name ?? "…"}</h1>
        <Link href="/admin/news/posts" className="text-sm text-indigo-300 underline">Посты</Link>
      </div>
      <NewsNav />
      {error && <p role="alert" className="text-sm text-red-400">{error}</p>}
      {notice && <p role="status" className="text-sm text-emerald-400">{notice}</p>}
      {flow?.last_error && <p className="text-sm text-amber-300">Последняя ошибка: {flow.last_error}</p>}
      {blockers.length > 0 && (
        <ul className="list-disc rounded-lg border border-amber-600/40 bg-amber-500/10 p-3 pl-8 text-sm text-amber-200">
          {blockers.map((b) => <li key={b}>{b}</li>)}
        </ul>
      )}

      {flow && (
        <FlowEditor
          initial={flow}
          saving={saving}
          onSave={async (draft) => {
            setSaving(true);
            setNotice("");
            try {
              setFlow(await updateNewsFlow(id, draft));
              setNotice("Поток сохранён.");
              await load();
            } finally {
              setSaving(false);
            }
          }}
        />
      )}

      <section className="rounded-xl border border-slate-700 bg-slate-800 p-4">
        <div className="mb-3 flex flex-wrap items-center gap-3">
          <h2 className="text-sm font-semibold">Сюжеты в очереди</h2>
          <button
            type="button"
            className="rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600 disabled:opacity-40"
            disabled={generating || !flow}
            onClick={async () => {
              setGenerating(true);
              setError("");
              setNotice("");
              try {
                const post = await generateNewsPost(id);
                setNotice(post ? "Черновик создан — он на странице «Посты»." : "Сейчас нечего публиковать.");
                await load();
              } catch (e) {
                setError(msg(e));
              } finally {
                setGenerating(false);
              }
            }}
          >
            {generating ? "Генерируем…" : "Создать пост сейчас"}
          </button>
          <button type="button" className="rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600" onClick={() => void load()}>Обновить</button>
        </div>
        <p className="mb-3 text-xs text-slate-400">Открытые сюжеты потока по убыванию оценки. «Создать пост сейчас» берёт лучший из них и игнорирует расписание.</p>
        {stories.length === 0 ? (
          <p className="text-sm text-slate-400">Пока нет подходящих сюжетов: нужно подождать загрузки источников или снизить порог релевантности.</p>
        ) : (
          <table className="w-full text-left text-sm">
            <thead className="text-xs uppercase tracking-wide text-slate-400"><tr><th className="py-2">Сюжет</th><th>Изданий</th><th>Статей</th><th>Релевантность</th><th>Оценка</th><th>Обновлён</th></tr></thead>
            <tbody>
              {stories.map((s) => (
                <tr key={s.id} className="border-t border-slate-700">
                  <td className="py-2 pr-3">{s.title}</td>
                  <td>{s.sources}</td><td>{s.articles}</td><td>{s.relevance.toFixed(2)}</td><td>{s.score.toFixed(1)}</td><td>{relativeTime(s.last_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  );
}
