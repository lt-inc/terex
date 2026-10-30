"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { approveClip, rejectClip, fetchClips } from "@/lib/api";
import { loadPendingRequest } from "@/lib/direct-publish";
import type { Clip } from "@/types";
import PreviewModal, {
  type PreviewTarget,
} from "@/components/media/PreviewModal";
import DirectPublishDialog from "@/components/clips/DirectPublishDialog";

const TABS = [
  { key: "pending", label: "Pending approval" },
  { key: "approved", label: "Approved" },
  { key: "rejected", label: "Rejected" },
] as const;

function fmt(sec: number) {
  const m = Math.floor(sec / 60);
  const s = Math.floor(sec % 60);
  return `${m}:${String(s).padStart(2, "0")}`;
}

export default function ClipsPage() {
  const [tab, setTab] = useState<(typeof TABS)[number]["key"]>("pending");
  const [clips, setClips] = useState<Clip[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busyIds, setBusyIds] = useState<Set<string>>(new Set());
  const [preview, setPreview] = useState<PreviewTarget | null>(null);
  const [publishing, setPublishing] = useState<{
    clip: Clip;
    section: "publish" | "history";
  } | null>(null);
  const [unconfirmed, setUnconfirmed] = useState<Set<string>>(new Set());

  useEffect(() => {
    let storage: Storage | null = null;
    try {
      storage = window.localStorage;
    } catch {
      // Browsers may block access to the storage getter itself.
    }
    setUnconfirmed(
      new Set(
        clips
          .filter((c) => loadPendingRequest(storage, c.clip_id))
          .map((c) => c.clip_id),
      ),
    );
  }, [clips, publishing]);
  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setClips(await fetchClips(tab));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load clips");
    } finally {
      setLoading(false);
    }
  }, [tab]);

  useEffect(() => {
    load();
  }, [load]);

  async function act(c: Clip, fn: (id: string) => Promise<void>) {
    setBusyIds((prev) => new Set(prev).add(c.clip_id));
    try {
      await fn(c.clip_id);
      setClips((prev) => prev.filter((x) => x.clip_id !== c.clip_id));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Action failed");
    } finally {
      setBusyIds((prev) => {
        const next = new Set(prev);
        next.delete(c.clip_id);
        return next;
      });
    }
  }

  return (
    <div className="p-6 flex flex-col gap-6 min-h-full">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-slate-100">Clips</h1>
        <button
          onClick={load}
          disabled={loading}
          className="bg-slate-700 hover:bg-slate-600 disabled:opacity-40 text-white text-sm font-medium px-4 py-2 rounded-lg border border-slate-600 transition-colors"
        >
          {loading ? "Loading…" : "Refresh"}
        </button>
      </div>

      <div className="flex gap-2">
        {TABS.map((t) => (
          <button
            key={t.key}
            onClick={() => setTab(t.key)}
            className={`text-sm px-3 py-1.5 rounded-lg border transition-colors ${tab === t.key ? "bg-indigo-600 border-indigo-500 text-white" : "bg-slate-800 border-slate-700 text-slate-300 hover:bg-slate-700"}`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {error && <p className="text-sm text-red-400">{error}</p>}
      {!loading && clips.length === 0 && (
        <p className="text-slate-500 text-sm">No clips here.</p>
      )}

      <div className="grid gap-4 grid-cols-1 md:grid-cols-2 xl:grid-cols-3">
        {clips.map((c) => (
          <div
            key={c.clip_id}
            className="bg-slate-800 border border-slate-700 rounded-xl overflow-hidden flex flex-col"
          >
            <div className="bg-black aspect-[9/16] max-h-96 flex items-center justify-center">
              <button
                className="flex h-full w-full flex-col items-center justify-center gap-3 text-sm text-slate-300 hover:bg-slate-900 disabled:text-slate-600"
                disabled={!!c.render_state && c.render_state !== "ready"}
                onClick={() =>
                  setPreview({
                    clipID: c.clip_id,
                    kind: "rendered",
                    title: c.title || c.clip_id,
                  })
                }
              >
                <span className="text-4xl">▶</span>
                <span>
                  {c.render_state && c.render_state !== "ready"
                    ? "Видео ещё готовится"
                    : "Смотреть видео"}
                </span>
                <span className="text-xs text-slate-500">
                  Загрузка начнётся после нажатия
                </span>
              </button>
            </div>
            <div className="p-3 flex flex-col gap-2">
              <Link
                href={`/admin/clips/${encodeURIComponent(c.clip_id)}`}
                className="text-indigo-400 text-sm"
              >
                Edit and review revision →
              </Link>
              <div className="flex flex-col gap-0.5">
                <span className="text-sm font-medium text-slate-100 truncate">
                  {c.title || c.clip_id}
                </span>
                <span className="text-xs text-slate-500 font-mono">
                  job {c.job_id} · {fmt(c.start_sec)}–{fmt(c.end_sec)}
                </span>
                {c.hashtags?.length > 0 && (
                  <span className="text-xs text-slate-400">
                    {c.hashtags.map((h) => `#${h.replace(/^#/, "")}`).join(" ")}
                  </span>
                )}
              </div>
              <div className="flex flex-wrap gap-2">
                <button
                  onClick={() => setPublishing({ clip: c, section: "publish" })}
                  className="flex-1 bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-medium px-3 py-1.5 rounded-lg transition-colors"
                >
                  Опубликовать напрямую
                </button>
                <button
                  onClick={() => setPublishing({ clip: c, section: "history" })}
                  className="bg-slate-700 hover:bg-slate-600 text-slate-200 text-sm px-3 py-1.5 rounded-lg border border-slate-600 transition-colors"
                >
                  Публикации
                </button>
              </div>
              {unconfirmed.has(c.clip_id) && (
                <p className="text-xs text-amber-300">
                  Запрос на публикацию не подтверждён — откройте «Опубликовать
                  напрямую» и повторите его.
                </p>
              )}
              {c.approval === "pending" && (
                <div className="flex gap-2">
                  <Link
                    href={`/admin/clips/${encodeURIComponent(c.clip_id)}`}
                    className="flex-1 bg-emerald-600 hover:bg-emerald-500 text-center text-white text-sm font-medium px-3 py-1.5 rounded-lg transition-colors"
                  >
                    {c.clip_id.startsWith("twitch-") || c.clip_id.startsWith("compilation-") ? "Редактор · автоматический допуск после рендера" : "Review & approve"}
                  </Link>
                  <button
                    disabled={busyIds.has(c.clip_id)}
                    onClick={() => act(c, rejectClip)}
                    className="flex-1 bg-slate-700 hover:bg-slate-600 disabled:opacity-40 text-red-300 text-sm font-medium px-3 py-1.5 rounded-lg border border-slate-600 transition-colors"
                  >
                    Reject
                  </button>
                </div>
              )}
            </div>
          </div>
        ))}
      </div>
      <PreviewModal target={preview} onClose={() => setPreview(null)} />
      {publishing && (
        <DirectPublishDialog
          key={publishing.clip.clip_id}
          clipID={publishing.clip.clip_id}
          clipTitle={publishing.clip.title || "Клип"}
          initialSection={publishing.section}
          onClose={() => setPublishing(null)}
        />
      )}
    </div>
  );
}
