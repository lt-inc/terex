"use client";
import { useCallback, useEffect, useState } from "react";
import { fetchNewsPostImages } from "@/lib/news";
import type { NewsImageCandidate, NewsImageEdit } from "@/types/news";

const input = "w-full rounded-lg border border-slate-600 bg-slate-700 px-3 py-2 text-sm";

export default function ImagePicker({ postId, initialQuery, busy, onPick, onClose }: {
  postId: number;
  initialQuery: string;
  busy: boolean;
  onPick: (image: NewsImageEdit) => void;
  onClose: () => void;
}) {
  const [query, setQuery] = useState(initialQuery);
  const [items, setItems] = useState<NewsImageCandidate[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [custom, setCustom] = useState("");

  const load = useCallback(async (q: string) => {
    setLoading(true);
    try {
      setItems((await fetchNewsPostImages(postId, q)) ?? []);
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [postId]);
  useEffect(() => {
    void load(initialQuery);
  }, [load, initialQuery]);

  return (
    <div className="rounded-xl border border-slate-600 bg-slate-900 p-3">
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <input className={`${input} max-w-xs`} value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Поиск фото: bitcoin chart"
          onKeyDown={(e) => { if (e.key === "Enter") void load(query); }} aria-label="Поиск фото" />
        <button type="button" className="rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600 disabled:opacity-40" disabled={loading} onClick={() => void load(query)}>Найти</button>
        <button type="button" className="ml-auto rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600" onClick={onClose}>Закрыть</button>
      </div>
      {error && <p role="alert" className="mb-2 text-sm text-red-400">{error}</p>}
      {loading ? <p className="text-sm text-slate-400">Ищем…</p> : items.length === 0 ? (
        <p className="text-sm text-slate-400">Ничего не найдено. Измените запрос (лучше по-английски) или вставьте ссылку ниже.</p>
      ) : (
        <ul className="grid grid-cols-2 gap-2 sm:grid-cols-3">
          {items.map((c) => (
            <li key={c.url}>
              <button type="button" disabled={busy} title={c.description}
                onClick={() => onPick({ url: c.url, source: c.source, credit: c.source === "pexels" ? c.credit : "", credit_url: c.credit_url })}
                className="group block w-full overflow-hidden rounded-lg border border-slate-700 text-left hover:border-indigo-500 disabled:opacity-40">
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img src={c.thumb} alt="" referrerPolicy="no-referrer" loading="lazy" className="h-24 w-full object-cover" />
                <span className="block truncate px-2 py-1 text-xs text-slate-400">{c.source === "article" ? `Из новости · ${c.credit}` : `${c.source === "pixabay" ? "Pixabay" : "Pexels"}${c.credit ? ` · ${c.credit}` : ""}`}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="mt-3 flex flex-wrap gap-2">
        <input className={`${input} flex-1`} value={custom} onChange={(e) => setCustom(e.target.value)} placeholder="Или ссылка на свою картинку: https://…" aria-label="Ссылка на картинку" />
        <button type="button" className="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white hover:bg-indigo-500 disabled:opacity-40" disabled={busy || !custom.trim()} onClick={() => onPick({ url: custom.trim() })}>Использовать</button>
      </div>
    </div>
  );
}
