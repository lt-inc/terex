"use client";
import { useSearchParams } from "next/navigation";
import { Suspense, useCallback, useEffect, useState } from "react";
import NewsNav from "@/components/news/NewsNav";
import { approveNewsPost, editNewsPost, fetchNewsFlows, fetchNewsPosts, rejectNewsPost } from "@/lib/news";
import { POST_STATUS_LABEL, POST_STATUS_STYLE, relativeTime } from "@/lib/news-flow";
import type { NewsFlow, NewsPost, NewsPostStatus } from "@/types/news";

const input = "w-full rounded-lg border border-slate-600 bg-slate-700 px-3 py-2 text-sm";
const button = "rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600 disabled:opacity-40";
const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));
const TABS: (NewsPostStatus | "")[] = ["pending", "approved", "published", "failed", "rejected", ""];

function PostCard({ post, busy, onAction }: { post: NewsPost; busy: boolean; onAction: (work: () => Promise<unknown>, done: string) => void }) {
  const editable = post.status === "pending" || post.status === "approved" || post.status === "failed";
  const [title, setTitle] = useState(post.title);
  const [text, setText] = useState(post.text);
  const [when, setWhen] = useState("");
  const dirty = title !== post.title || text !== post.text;
  useEffect(() => {
    setTitle(post.title);
    setText(post.text);
  }, [post.title, post.text]);

  // Saving edits first guarantees that what is approved is what the operator is looking at.
  const withEdits = async (next: () => Promise<unknown>) => {
    if (dirty) await editNewsPost(post.id, { title, text });
    return next();
  };

  return (
    <article className="flex flex-col gap-3 rounded-xl border border-slate-700 bg-slate-800 p-4">
      <div className="flex flex-wrap items-center gap-3 text-xs text-slate-400">
        <span className={`rounded-full px-2 py-0.5 ${POST_STATUS_STYLE[post.status]}`}>{POST_STATUS_LABEL[post.status]}</span>
        <span>{post.flow_name}</span>
        <span>создан {relativeTime(post.created_at)}</span>
        <span>оценка {post.score.toFixed(1)}</span>
        {post.scheduled_at && post.status === "approved" && <span>в {new Date(post.scheduled_at).toLocaleString("ru-RU")}</span>}
        {post.published_at && <span>опубликован {new Date(post.published_at).toLocaleString("ru-RU")}</span>}
      </div>
      {editable ? (
        <>
          <input className={`${input} font-semibold`} value={title} onChange={(e) => setTitle(e.target.value)} maxLength={200} aria-label="Заголовок" />
          <textarea className={input} rows={Math.min(14, Math.max(4, text.split("\n").length + 2))} value={text} onChange={(e) => setText(e.target.value)} aria-label="Текст поста" />
          <p className={`text-xs ${text.length > 3800 ? "text-red-400" : "text-slate-500"}`}>{text.length} / 3800 символов</p>
        </>
      ) : (
        <div>
          <h3 className="font-semibold">{post.title}</h3>
          <p className="mt-2 whitespace-pre-wrap text-sm text-slate-200">{post.text}</p>
        </div>
      )}
      {post.links.length > 0 && (
        <p className="text-xs text-slate-400">
          Источники:{" "}
          {post.links.map((l, i) => (
            <span key={l.url}>{i > 0 && " · "}<a className="text-indigo-300 underline" href={l.url} target="_blank" rel="noopener noreferrer">{l.title}</a></span>
          ))}
        </p>
      )}
      {post.error && <p className="text-sm text-red-400">{post.error}</p>}
      <div className="flex flex-wrap items-center gap-2">
        {(post.status === "pending" || post.status === "failed") && (
          <>
            <button type="button" className="rounded-lg bg-emerald-600 px-4 py-2 text-sm text-white hover:bg-emerald-500 disabled:opacity-40" disabled={busy || !text.trim()}
              onClick={() => onAction(() => withEdits(() => approveNewsPost(post.id)), post.status === "failed" ? "Повторная отправка выполнена." : "Пост отправлен в канал.")}>
              {post.status === "failed" ? "Повторить" : "Опубликовать"}
            </button>
            <input type="datetime-local" className="rounded-lg border border-slate-600 bg-slate-700 px-2 py-2 text-sm" value={when} onChange={(e) => setWhen(e.target.value)} aria-label="Время публикации" />
            <button type="button" className={button} disabled={busy || !when || !text.trim()}
              onClick={() => onAction(() => withEdits(() => approveNewsPost(post.id, new Date(when).toISOString())), "Публикация запланирована.")}>
              Запланировать
            </button>
          </>
        )}
        {editable && dirty && (
          <button type="button" className={button} disabled={busy} onClick={() => onAction(() => editNewsPost(post.id, { title, text }), "Правки сохранены.")}>Сохранить правки</button>
        )}
        {editable && (
          <button type="button" className="ml-auto rounded-lg bg-red-900/60 px-3 py-2 text-sm text-red-200 hover:bg-red-800 disabled:opacity-40" disabled={busy}
            onClick={() => onAction(() => rejectNewsPost(post.id), "Пост отклонён.")}>
            Отклонить
          </button>
        )}
      </div>
    </article>
  );
}

function Posts() {
  const params = useSearchParams();
  const [flowID, setFlowID] = useState(Number(params.get("flow")) || 0);
  const [status, setStatus] = useState<NewsPostStatus | "">("pending");
  const [posts, setPosts] = useState<NewsPost[]>([]);
  const [flows, setFlows] = useState<NewsFlow[]>([]);
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setPosts((await fetchNewsPosts(status, flowID)) ?? []);
      setError("");
    } catch (e) {
      setError(msg(e));
    } finally {
      setLoading(false);
    }
  }, [status, flowID]);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    void fetchNewsFlows().then((f) => setFlows(f ?? [])).catch(() => undefined);
  }, []);

  async function act(work: () => Promise<unknown>, done: string) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await work();
      setNotice(done);
    } catch (e) {
      setError(msg(e));
    } finally {
      setBusy(false);
      await load();
    }
  }

  return (
    <div className="flex flex-col gap-5 p-4 sm:p-6">
      <h1 className="text-xl font-semibold">Посты</h1>
      <NewsNav />
      <div className="flex flex-wrap items-center gap-2">
        {TABS.map((s) => (
          <button key={s || "all"} type="button" onClick={() => setStatus(s)}
            className={`rounded-lg border px-3 py-2 text-sm ${status === s ? "border-indigo-500 bg-indigo-600 text-white" : "border-slate-700 bg-slate-800 text-slate-300 hover:bg-slate-700"}`}>
            {s ? POST_STATUS_LABEL[s] : "Все"}
          </button>
        ))}
        <select className="rounded-lg border border-slate-600 bg-slate-700 px-3 py-2 text-sm" value={flowID} onChange={(e) => setFlowID(Number(e.target.value))} aria-label="Поток">
          <option value={0}>Все потоки</option>
          {flows.map((f) => <option key={f.id} value={f.id}>{f.name}</option>)}
        </select>
        <button type="button" className={button} onClick={() => void load()} disabled={loading}>{loading ? "Загружаем…" : "Обновить"}</button>
      </div>
      {error && <p role="alert" className="text-sm text-red-400">{error}</p>}
      {notice && <p role="status" className="text-sm text-emerald-400">{notice}</p>}
      {!loading && posts.length === 0 && <p className="text-sm text-slate-400">В этом разделе постов нет.</p>}
      <div className="grid gap-4 xl:grid-cols-2">
        {posts.map((p) => <PostCard key={p.id} post={p} busy={busy} onAction={(work, done) => void act(work, done)} />)}
      </div>
    </div>
  );
}

export default function NewsPostsPage() {
  return (
    <Suspense fallback={<p className="p-6 text-slate-400">Загрузка…</p>}>
      <Posts />
    </Suspense>
  );
}
