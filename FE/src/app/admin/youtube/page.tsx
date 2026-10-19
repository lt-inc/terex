"use client";
import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import YouTubeNav from "@/components/youtube/YouTubeNav";
import ConfirmAction from "@/components/ui/ConfirmAction";
import Toggle from "@/components/ui/Toggle";
import { relativeTime } from "@/lib/news-flow";
import { deleteYTFlow, fetchYTFlows, fetchYTQuota, runYTFlow, STATUS_LABEL, updateYTFlow } from "@/lib/youtube";
import type { YTFlow, YTQuota } from "@/types/youtube";

const button = "rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600 disabled:opacity-40";
const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));

export default function YouTubeFlowsPage() {
  const [flows, setFlows] = useState<YTFlow[]>([]);
  const [quota, setQuota] = useState<YTQuota | null>(null);
  const [remove, setRemove] = useState<YTFlow | null>(null);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  const load = useCallback(async () => {
    const [f, q] = await Promise.allSettled([fetchYTFlows(), fetchYTQuota()]);
    if (f.status === "fulfilled") setFlows(f.value ?? []);
    if (q.status === "fulfilled") setQuota(q.value);
    setError([f, q].flatMap((r) => (r.status === "rejected" ? [msg(r.reason)] : [])).join(" · "));
  }, []);
  useEffect(() => {
    void load();
    const timer = setInterval(() => void load(), 15000);
    return () => clearInterval(timer);
  }, [load]);

  async function run(id: string, work: () => Promise<string | void>) {
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
      setBusy("");
    }
  }

  return (
    <div className="flex flex-col gap-5 p-4 sm:p-6">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">YouTube-потоки</h1>
        <Link href="/admin/youtube/new" className="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white hover:bg-indigo-500">Новый поток</Link>
        <button type="button" className={button} onClick={() => void load()}>Обновить</button>
      </div>
      <YouTubeNav />
      {error && <p role="alert" className="text-sm text-red-400">{error}</p>}
      {notice && <p role="status" className="text-sm text-emerald-400">{notice}</p>}

      {quota && (
        <section className="rounded-xl border border-slate-700 bg-slate-800 p-4 text-sm text-slate-300">
          <p>
            Квота API сегодня (тихоокеанские сутки {quota.day}): <b>{quota.used}</b> из пула <b>{quota.pool}</b> единиц
            {quota.pool === 0 && <span className="text-red-400"> — пул пуст, проверьте YT_QUOTA_* в настройках</span>}
          </p>
          <div className="mt-2 h-2 overflow-hidden rounded bg-slate-700">
            <div className={`h-full ${quota.tripped ? "bg-red-500" : "bg-indigo-500"}`} style={{ width: `${quota.pool ? Math.min(100, (quota.used / quota.pool) * 100) : 0}%` }} />
          </div>
          {quota.tripped && <p className="mt-2 text-amber-300">Google вернул quotaExceeded: чтение остановлено до конца суток. Публикация ждущих видео продолжается. {quota.trip_note}</p>}
        </section>
      )}

      {flows.length === 0 && <p className="text-sm text-slate-400">Потоков пока нет. Создайте первый и добавьте каналы-источники или поисковые запросы.</p>}
      <div className="grid gap-4 lg:grid-cols-2">
        {flows.map((f) => (
          <article key={f.id} className="flex flex-col gap-3 rounded-xl border border-slate-700 bg-slate-800 p-4">
            <div className="flex items-start gap-3">
              <div className="flex-1">
                <h2 className="font-medium">{f.name}</h2>
                <p className="text-xs text-slate-400">на канал {f.account_name || "—"}</p>
              </div>
              <div className="w-40 shrink-0">
                <Toggle label={f.enabled ? "Включён" : "Выключен"} checked={f.enabled} disabled={busy === f.id}
                  onChange={(v) => void run(f.id, async () => { await updateYTFlow(f.id, { enabled: v }); })} />
              </div>
            </div>
            <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs text-slate-400">
              <dt>Статус</dt><dd className={f.last_status === "ok" || !f.last_status ? "text-slate-200" : "text-amber-300"}>{STATUS_LABEL[f.last_status] ?? f.last_status}</dd>
              <dt>Источников</dt><dd className="text-slate-200">{f.sources.length}</dd>
              <dt>Ждут / в работе</dt><dd className="text-slate-200">{f.counts.new} / {f.counts.queued}</dd>
              <dt>Опубликовано / ошибки</dt><dd className="text-slate-200">{f.counts.done} / {f.counts.failed}</dd>
              <dt>Квота сегодня</dt><dd className="text-slate-200">{f.quota_used} / {f.config.quota_daily}</dd>
              <dt>Следующая публикация</dt><dd className="text-slate-200">{f.next_slot_at ? new Date(f.next_slot_at).toLocaleString() : "—"}</dd>
              <dt>Последний проход</dt><dd className="text-slate-200">{relativeTime(f.last_run_at)}</dd>
            </dl>
            {f.last_error && <p className="text-xs text-amber-300">{f.last_error}</p>}
            <div className="flex flex-wrap gap-2">
              <Link href={`/admin/youtube/${f.id}`} className={button}>Настроить</Link>
              <button type="button" className={button} disabled={busy === f.id || !f.enabled}
                onClick={() => void run(f.id, async () => { await runYTFlow(f.id); return "Проход выполнен."; })}>
                {busy === f.id ? "Работаем…" : "Проверить сейчас"}
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
          message={`«${remove.name}» будет удалён вместе с найденными видео. Уже созданные задания и опубликованные ролики останутся.`}
          confirmLabel="Удалить"
          onCancel={() => setRemove(null)}
          onConfirm={() => {
            const target = remove;
            setRemove(null);
            void run(target.id, async () => { await deleteYTFlow(target.id); return "Поток удалён."; });
          }}
        />
      )}
    </div>
  );
}
