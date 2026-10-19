"use client";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import FlowForm from "@/components/youtube/FlowForm";
import YouTubeNav from "@/components/youtube/YouTubeNav";
import { relativeTime } from "@/lib/news-flow";
import { addYTSource, deleteYTSource, fetchYTCandidates, fetchYTFlow, skipYTCandidate, STATE_LABEL, updateYTFlow } from "@/lib/youtube";
import type { YTCandidate, YTCandidateState, YTFlow } from "@/types/youtube";

const input = "rounded-lg border border-slate-600 bg-slate-700 px-3 py-2 text-sm";
const button = "rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600 disabled:opacity-40";
const card = "rounded-xl border border-slate-700 bg-slate-800 p-4";
const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));
const STATES: YTCandidateState[] = ["new", "queued", "done", "failed", "skipped"];

export default function EditYouTubeFlowPage() {
  const { id } = useParams<{ id: string }>();
  const [flow, setFlow] = useState<YTFlow | undefined>();
  const [candidates, setCandidates] = useState<YTCandidate[]>([]);
  const [state, setState] = useState<YTCandidateState>("new");
  const [kind, setKind] = useState<"channel" | "search">("channel");
  const [value, setValue] = useState("");
  const [region, setRegion] = useState("");
  const [language, setLanguage] = useState("");
  const [saving, setSaving] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  const load = useCallback(async () => {
    try {
      const [f, c] = await Promise.all([fetchYTFlow(id), fetchYTCandidates(id, state)]);
      setFlow(f);
      setCandidates(c ?? []);
      setError("");
    } catch (e) {
      setError(msg(e));
    }
  }, [id, state]);
  useEffect(() => {
    void load();
  }, [load]);

  async function act(work: () => Promise<unknown>, done?: string) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await work();
      if (done) setNotice(done);
      await load();
    } catch (e) {
      setError(msg(e));
    } finally {
      setBusy(false);
    }
  }

  if (!flow) return <div className="p-6">{error ? <p className="text-red-400">{error}</p> : <p className="text-slate-400">Загрузка…</p>}</div>;

  return (
    <div className="flex flex-col gap-5 p-4 sm:p-6">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">{flow.name}</h1>
        <Link href="/admin/youtube" className={button}>К потокам</Link>
      </div>
      <YouTubeNav />
      {error && <p role="alert" className="text-sm text-red-400">{error}</p>}
      {notice && <p role="status" className="text-sm text-emerald-400">{notice}</p>}

      <section className={card}>
        <h2 className="mb-3 font-medium">Источники</h2>
        <ul className="mb-4 flex flex-col gap-2 text-sm">
          {flow.sources.length === 0 && <li className="text-slate-400">Источников нет — поток ничего не найдёт.</li>}
          {flow.sources.map((s) => (
            <li key={s.id} className="flex flex-wrap items-center gap-3 rounded-lg bg-slate-900/40 px-3 py-2">
              <span className="rounded bg-slate-700 px-2 py-0.5 text-xs">{s.kind === "channel" ? "канал" : "поиск"}</span>
              <span className="flex-1 break-all">{s.title || s.value}</span>
              {s.kind === "search" && (s.params.region_code || s.params.language) && <span className="text-xs text-slate-400">{s.params.region_code} {s.params.language}</span>}
              <span className="text-xs text-slate-400">обход: {relativeTime(s.last_scan_at)}</span>
              {s.last_error && <span className="text-xs text-amber-300">{s.last_error}</span>}
              <button type="button" className="text-xs text-red-300 hover:text-red-200" disabled={busy} onClick={() => void act(() => deleteYTSource(flow.id, s.id))}>Убрать</button>
            </li>
          ))}
        </ul>
        <div className="flex flex-wrap items-end gap-2">
          <select className={input} value={kind} onChange={(e) => setKind(e.target.value as "channel" | "search")}>
            <option value="channel">Канал (ссылка, @handle или UC-id)</option>
            <option value="search">Поисковый запрос (весь YouTube)</option>
          </select>
          <input className={`${input} min-w-64 flex-1`} placeholder={kind === "channel" ? "https://www.youtube.com/@channel" : "funny cats"} value={value} onChange={(e) => setValue(e.target.value)} />
          {kind === "search" && (
            <>
              <input className={`${input} w-24`} placeholder="Регион US" maxLength={2} value={region} onChange={(e) => setRegion(e.target.value.toUpperCase())} />
              <input className={`${input} w-24`} placeholder="Язык en" maxLength={5} value={language} onChange={(e) => setLanguage(e.target.value)} />
            </>
          )}
          <button type="button" className={button} disabled={busy || !value.trim()}
            onClick={() => void act(async () => { await addYTSource(flow.id, { kind, value, region_code: region || undefined, language: language || undefined }); setValue(""); }, "Источник добавлен.")}>
            Добавить
          </button>
        </div>
        {kind === "search" && <p className="mt-2 text-xs text-slate-500">Один поиск стоит 100 единиц квоты — он расходует бюджет потока быстрее, чем обход каналов (около 2 единиц).</p>}
      </section>

      <FlowForm
        saving={saving}
        initial={{ name: flow.name, account_id: flow.account_id, config: flow.config }}
        onSave={async (draft) => {
          setSaving(true);
          try {
            setFlow(await updateYTFlow(flow.id, draft));
            setNotice("Сохранено.");
          } finally {
            setSaving(false);
          }
        }}
      />

      <section className={card}>
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <h2 className="mr-2 font-medium">Найденные видео</h2>
          {STATES.map((s) => (
            <button key={s} type="button" onClick={() => setState(s)}
              className={`rounded-lg border px-3 py-1.5 text-sm ${state === s ? "border-indigo-500 bg-indigo-600 text-white" : "border-slate-700 bg-slate-800 text-slate-300 hover:bg-slate-700"}`}>
              {STATE_LABEL[s]} ({flow.counts[s]})
            </button>
          ))}
        </div>
        {candidates.length === 0 && <p className="text-sm text-slate-400">Здесь пока пусто.</p>}
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead className="text-xs text-slate-400">
              <tr><th className="py-1 pr-3">Видео</th><th className="pr-3">Канал</th><th className="pr-3">Просмотры</th><th className="pr-3">В час</th><th className="pr-3">Опубликовано</th><th className="pr-3">Слот</th><th /></tr>
            </thead>
            <tbody>
              {candidates.map((c) => (
                <tr key={c.video_id} className="border-t border-slate-700 align-top">
                  <td className="py-2 pr-3">
                    <a className="text-indigo-300 hover:underline" href={`https://www.youtube.com/shorts/${c.video_id}`} target="_blank" rel="noreferrer">{c.title || c.video_id}</a>
                    {c.reason && <div className="text-xs text-amber-300">{c.reason}</div>}
                  </td>
                  <td className="pr-3 text-slate-300">{c.channel_title}</td>
                  <td className="pr-3">{c.views.toLocaleString()}</td>
                  <td className="pr-3">{Math.round(c.score).toLocaleString()}</td>
                  <td className="pr-3 text-slate-400">{relativeTime(c.published_at)}</td>
                  <td className="pr-3 text-slate-400">{c.slot_at ? new Date(c.slot_at).toLocaleString() : "—"}</td>
                  <td>{c.state === "new" && <button type="button" className="text-xs text-red-300 hover:text-red-200" disabled={busy} onClick={() => void act(() => skipYTCandidate(flow.id, c.video_id))}>Пропустить</button>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}
