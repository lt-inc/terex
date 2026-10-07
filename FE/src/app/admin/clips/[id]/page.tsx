"use client";
import { useCallback, useEffect, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import Toggle from "@/components/ui/Toggle";
import ConfirmAction from "@/components/ui/ConfirmAction";
import {
  approveClip,
  ApiError,
  fetchClipEditor,
  saveClipEditor,
  retryClipRender,
  restoreClipRevision,
} from "@/lib/api";
import { validateEditor } from "@/lib/validation";
import type { ClipEditor, SubtitleCue } from "@/types";
import PreviewModal, {
  type PreviewTarget,
} from "@/components/media/PreviewModal";
const input =
  "bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 w-full min-w-0";
const button =
  "bg-slate-700 hover:bg-slate-600 disabled:opacity-40 px-3 py-2 rounded-lg text-sm";
function spec(e: ClipEditor) {
  return JSON.stringify({
    start_sec: e.start_sec,
    end_sec: e.end_sec,
    title: e.title,
    description: e.description,
    visibility: e.visibility ?? "public",
    hashtags: e.hashtags,
    cues: e.cues,
    subtitles_enabled: e.subtitles_enabled,
  });
}
export default function ClipEditorPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const [confirmation, setConfirmation] = useState<{
    kind: "approve" | "restore" | "reload" | "leave";
    url?: string;
  } | null>(null);
  const [preview, setPreview] = useState<PreviewTarget | null>(null);
  const [saved, setSaved] = useState<ClipEditor | null>(null),
    [draft, setDraft] = useState<ClipEditor | null>(null),
    [tags, setTags] = useState(""),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [notice, setNotice] = useState(""),
    [current, setCurrent] = useState(0);
  const [restoreID, setRestoreID] = useState("");
  const parsedTags = tags
    .split(/[\s,]+/)
    .map((t) => t.replace(/^#+/, ""))
    .filter(Boolean);
  const dirty = !!(
    saved &&
    draft &&
    spec({ ...draft, hashtags: parsedTags }) !== spec(saved)
  );
  const load = useCallback(async () => {
    setError("");
    try {
      const e = await fetchClipEditor(id);
      setSaved(e);
      setDraft(e);
      setTags((e.hashtags ?? []).join(" "));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [id]);
  useEffect(() => {
    load();
  }, [load]);
  useEffect(() => {
    if (
      !saved ||
      !["pending", "queued", "rendering"].includes(saved.render_state)
    )
      return;
    let active = true;
    const timer = setInterval(() => {
      fetchClipEditor(id)
        .then((e) => {
          if (!active) return;
          setSaved(e);
          if (!dirty) {
            setDraft(e);
            setTags((e.hashtags ?? []).join(" "));
          }
        })
        .catch((e) => {
          if (active) setError(e.message);
        });
    }, 3000);
    return () => {
      active = false;
      clearInterval(timer);
    };
  }, [id, saved?.render_state, dirty]);
  useEffect(() => {
    if (!dirty) return;
    function warn(e: BeforeUnloadEvent) {
      e.preventDefault();
    }
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);
  function change<K extends keyof ClipEditor>(key: K, value: ClipEditor[K]) {
    setDraft((d) => (d ? { ...d, [key]: value } : d));
  }
  function cueChange(id: string, patch: Partial<SubtitleCue>) {
    setDraft((d) =>
      d
        ? {
            ...d,
            cues: d.cues.map((c) => (c.id === id ? { ...c, ...patch } : c)),
          }
        : d,
    );
  }
  async function save() {
    if (!draft) return;
    const candidate = { ...draft, hashtags: parsedTags };
    const invalid = validateEditor(candidate);
    if (invalid) {
      setError(invalid);
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await saveClipEditor(id, candidate);
      setSaved(result);
      setDraft(result);
      setTags((result.hashtags ?? []).join(" "));
      setNotice(
        "Saved. Future publications are held until this revision is rendered and approved.",
      );
    } catch (e) {
      setError(
        e instanceof ApiError && e.status === 409
          ? "Version conflict. Your edits are preserved; reload the latest revision before applying them again."
          : e instanceof Error
            ? e.message
            : String(e),
      );
    } finally {
      setBusy(false);
    }
  }
  async function approve() {
    if (!saved || dirty) return;
    setBusy(true);
    setError("");
    try {
      await approveClip(id, saved.version);
      await load();
      setNotice("Revision approved. Due publications catch up automatically.");
    } catch (e) {
      setError(
        e instanceof ApiError && e.status === 409
          ? "Revision changed. Refresh and review before approving."
          : e instanceof Error
            ? e.message
            : String(e),
      );
    } finally {
      setBusy(false);
    }
  }
  async function retryRender() {
    if (!saved) return;
    setBusy(true);
    setError("");
    try {
      const result = await retryClipRender(id, saved.version);
      setSaved(result);
      if (!dirty) setDraft(result);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  function seek(value: number) {
    setCurrent(value);
    setPreview({
      clipID: id,
      revisionID: saved?.revision_id,
      kind: "source",
      title: draft?.title || id,
      initialTime: value,
    });
  }
  async function restore() {
    if (!saved || !restoreID || dirty) return;
    setBusy(true);
    setError("");
    try {
      const result = await restoreClipRevision(
        id,
        saved.version,
        Number(restoreID),
      );
      setSaved(result);
      setDraft(result);
      setTags((result.hashtags ?? []).join(" "));
      setRestoreID("");
      setNotice(
        "Previous render restored as a new revision. Review and approve to resume future publications.",
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  if (!draft)
    return (
      <div className="p-6">
        <Link href="/admin/clips" className="text-indigo-400">
          ← Clips
        </Link>
        <p className="my-4 text-red-400">{error || "Loading clip…"}</p>
        <button className={button} onClick={load}>
          Retry
        </button>
      </div>
    );
  return (
    <div className="p-4 sm:p-6 flex flex-col gap-5">
      <div className="flex justify-between">
        <Link
          className="text-indigo-400 text-sm"
          href="/admin/clips"
          onClick={(e) => {
            if (dirty) {
              e.preventDefault();
              setConfirmation({ kind: "leave", url: "/admin/clips" });
            }
          }}
        >
          ← Clips
        </Link>
        <Link
          href="/admin/calendar"
          className="text-indigo-400 text-sm"
          onClick={(e) => {
            if (dirty) {
              e.preventDefault();
              setConfirmation({ kind: "leave", url: "/admin/calendar" });
            }
          }}
        >
          Calendar →
        </Link>
      </div>
      <div className="flex justify-between gap-4 flex-wrap">
        <div>
          <h1 className="text-xl font-semibold">Clip editor</h1>
          <p className="text-xs text-slate-500">
            {id} · revision {saved?.version} · {saved?.render_state}
            {dirty ? " · unsaved edits" : ""}
          </p>
        </div>
        <div className="flex gap-2 flex-wrap">
          <button
            className={button}
            disabled={busy}
            onClick={() => {
              if (dirty) setConfirmation({ kind: "reload" });
              else load();
            }}
          >
            Reload
          </button>
          <button
            className="bg-indigo-600 disabled:opacity-40 px-3 py-2 rounded-lg text-sm"
            disabled={busy || !dirty}
            onClick={save}
          >
            {busy ? "Working…" : "Save & render"}
          </button>
          <button
            className="bg-emerald-600 disabled:opacity-40 px-3 py-2 rounded-lg text-sm"
            disabled={busy || dirty || saved?.render_state !== "ready"}
            onClick={() => setConfirmation({ kind: "approve" })}
          >
            Approve revision
          </button>
        </div>
      </div>
      <p className="text-sm text-slate-400">
        Saving this variant holds future publications until reapproval. Already
        started uploads keep their original revision. Subtitle times refer to
        the source video.
      </p>
      {error && (
        <p role="alert" className="text-red-400 text-sm">
          {error}
        </p>
      )}
      {notice && (
        <p role="status" className="text-emerald-400 text-sm">
          {notice}
        </p>
      )}
      {saved?.error && (
        <p className="text-red-400 text-sm">Render: {saved.error}</p>
      )}
      {saved?.render_state === "failed" && (
        <button
          disabled={busy || dirty || draft.source_available === false}
          className={button}
          onClick={retryRender}
        >
          Retry render of saved revision
        </button>
      )}
      {draft.source_available === false && (
        <p className="text-amber-400 text-sm">
          {draft.source_error ||
            "Original source is unavailable. You can update metadata; boundaries and subtitles are disabled."}
        </p>
      )}
      <div className="grid grid-cols-1 xl:grid-cols-2 gap-5">
        <section className="bg-slate-800 border border-slate-700 rounded-xl p-4 flex flex-col gap-3">
          <h2 className="text-sm font-semibold text-slate-300">
            Source and boundaries
          </h2>
          {draft.source_available !== false ? (
            <button
              className={`${button} min-h-32`}
              onClick={() => seek(current)}
            >
              ▶ Смотреть исходник и выбрать границы
            </button>
          ) : (
            <p className="text-amber-400 text-sm">
              Source unavailable. Reload when storage is available.
            </p>
          )}
          <p className="text-xs text-slate-400">
            Playhead {current.toFixed(2)}s · selected{" "}
            {(draft.end_sec - draft.start_sec).toFixed(2)}s · source{" "}
            {draft.source_duration_sec?.toFixed(2) ?? "?"}s
          </p>
          <div className="grid grid-cols-2 gap-3">
            {(["start_sec", "end_sec"] as const).map((key) => (
              <label
                key={key}
                className="flex flex-col gap-1 text-xs text-slate-400"
              >
                {key === "start_sec" ? "Start" : "End"} (seconds)
                <input
                  disabled={draft.source_available === false || busy}
                  className={input}
                  type="number"
                  min={0}
                  step={0.01}
                  value={draft[key]}
                  onChange={(e) => change(key, Number(e.target.value))}
                />
                <div className="flex gap-2">
                  <button
                    className="text-indigo-400"
                    disabled={draft.source_available === false || busy}
                    onClick={() => change(key, Math.round(current * 100) / 100)}
                  >
                    Use playhead
                  </button>
                  <button
                    className="text-indigo-400"
                    onClick={() => seek(draft[key])}
                  >
                    Seek
                  </button>
                </div>
                {!!draft.source_duration_sec && (
                  <input
                    aria-label={`${key} slider`}
                    type="range"
                    disabled={draft.source_available === false || busy}
                    min={0}
                    max={draft.source_duration_sec}
                    step={0.1}
                    value={draft[key]}
                    onChange={(e) => change(key, Number(e.target.value))}
                  />
                )}
              </label>
            ))}
          </div>
          <h2 className="text-sm font-semibold text-slate-300 mt-2">
            Rendered revision
          </h2>
          {!!saved?.revisions?.length && (
            <div className="flex gap-2">
              <select
                aria-label="Previous revision to restore"
                className={input}
                value={restoreID}
                disabled={busy || dirty}
                onChange={(e) => setRestoreID(e.target.value)}
              >
                <option value="">Choose an earlier ready render</option>
                {saved.revisions
                  .filter(
                    (r) =>
                      r.render_state === "ready" &&
                      r.revision_id !== saved.revision_id,
                  )
                  .map((r) => (
                    <option key={r.revision_id} value={r.revision_id}>
                      Revision {r.version}
                    </option>
                  ))}
              </select>
              <button
                className={button}
                disabled={busy || dirty || !restoreID}
                onClick={() => setConfirmation({ kind: "restore" })}
              >
                Restore
              </button>
            </div>
          )}
          {saved?.render_state === "ready" ? (
            <button
              className={`${button} min-h-24`}
              onClick={() =>
                setPreview({
                  clipID: id,
                  revisionID: saved.revision_id,
                  kind: "rendered",
                  title: saved.title || id,
                })
              }
            >
              ▶ Смотреть готовую версию
            </button>
          ) : (
            <p className="text-sm text-slate-500">
              {saved?.render_state === "failed"
                ? "Render failed; edit and save again."
                : saved?.render_state === "expired"
                  ? "Файл удалён по сроку хранения. История и ссылки на публикации сохранены; просмотр, восстановление и новый монтаж этой версии недоступны."
                : "The render appears here when ready."}
            </p>
          )}
        </section>
        <section className="bg-slate-800 border border-slate-700 rounded-xl p-4 flex flex-col gap-4">
          <h2 className="text-sm font-semibold text-slate-300">
            Publication metadata
          </h2>
          <label className="flex flex-col gap-1 text-xs text-slate-400">
            Visibility after processing
            <select
              className={input}
              value={draft.visibility || "public"}
              onChange={(e) =>
                change(
                  "visibility",
                  e.target.value as "public" | "unlisted" | "private",
                )
              }
            >
              <option value="public">Public</option>
              <option value="unlisted">Unlisted</option>
              <option value="private">Private</option>
            </select>
          </label>
          <label className="flex flex-col gap-1 text-xs text-slate-400">
            Title ({draft.title.length}/100)
            <input
              className={input}
              value={draft.title}
              maxLength={100}
              onChange={(e) => change("title", e.target.value)}
            />
          </label>
          <label className="flex flex-col gap-1 text-xs text-slate-400">
            Description
            <textarea
              className={input}
              rows={4}
              value={draft.description}
              maxLength={5000}
              onChange={(e) => change("description", e.target.value)}
            />
          </label>
          <label className="flex flex-col gap-1 text-xs text-slate-400">
            Hashtags separated by spaces
            <input
              className={input}
              value={tags}
              onChange={(e) => setTags(e.target.value)}
            />
          </label>
          <Toggle
            disabled={draft.source_available === false || busy}
            label="Burn subtitles into this revision"
            checked={draft.subtitles_enabled}
            onChange={(v) => change("subtitles_enabled", v)}
          />
          <div className="flex justify-between">
            <h2 className="text-sm font-semibold text-slate-300">
              Subtitle cues
            </h2>
            <button
              className="text-indigo-400 text-xs"
              disabled={draft.source_available === false || busy}
              onClick={() => {
                const start = Math.min(
                  draft.end_sec - 0.1,
                  Math.max(draft.start_sec, current),
                );
                change("cues", [
                  ...draft.cues,
                  {
                    id: crypto.randomUUID(),
                    start,
                    end: Math.min(draft.end_sec, start + 2),
                    text: "",
                  },
                ]);
              }}
            >
              + Add cue
            </button>
          </div>
          <div className="max-h-[600px] overflow-y-auto flex flex-col gap-3">
            {draft.cues.map((c) => (
              <div
                key={c.id}
                className="border border-slate-700 rounded-lg p-3 flex flex-col gap-2"
              >
                <div className="grid grid-cols-[1fr_1fr_auto_auto] gap-2 items-center">
                  <input
                    aria-label="Cue start seconds"
                    disabled={draft.source_available === false || busy}
                    className={input}
                    type="number"
                    min={0}
                    step={0.01}
                    value={c.start}
                    onChange={(e) =>
                      cueChange(c.id, { start: Number(e.target.value) })
                    }
                  />
                  <input
                    aria-label="Cue end seconds"
                    disabled={draft.source_available === false || busy}
                    className={input}
                    type="number"
                    min={0}
                    step={0.01}
                    value={c.end}
                    onChange={(e) =>
                      cueChange(c.id, { end: Number(e.target.value) })
                    }
                  />
                  <button
                    className="text-indigo-400 text-xs"
                    onClick={() => seek(c.start)}
                  >
                    Seek
                  </button>
                  <button
                    aria-label="Remove cue"
                    disabled={draft.source_available === false || busy}
                    className="text-red-400"
                    onClick={() =>
                      change(
                        "cues",
                        draft.cues.filter((x) => x.id !== c.id),
                      )
                    }
                  >
                    ×
                  </button>
                </div>
                <textarea
                  aria-label="Subtitle text"
                  disabled={draft.source_available === false || busy}
                  className={input}
                  rows={2}
                  value={c.text}
                  onChange={(e) => cueChange(c.id, { text: e.target.value })}
                />
                {(c.end <= draft.start_sec || c.start >= draft.end_sec) && (
                  <p className="text-xs text-slate-500">
                    Outside selection; excluded from render.
                  </p>
                )}
              </div>
            ))}
          </div>
        </section>
      </div>
      <PreviewModal
        target={preview}
        onClose={() => setPreview(null)}
        onTimeUpdate={preview?.kind === "source" ? setCurrent : undefined}
        onDuration={
          preview?.kind === "source"
            ? (duration) => {
                if (!draft.source_duration_sec)
                  change("source_duration_sec", duration);
              }
            : undefined
        }
      />
      {confirmation && (
        <ConfirmAction
          title={
            confirmation.kind === "approve"
              ? "Approve revision"
              : confirmation.kind === "restore"
                ? "Restore previous render"
                : "Discard unsaved edits"
          }
          message={
            confirmation.kind === "approve"
              ? `Approve revision ${saved?.version} and resume its future publications with ${draft.visibility || "public"} visibility?`
              : confirmation.kind === "restore"
                ? "Restore this earlier render as a new unapproved revision? Future publications remain held until approval."
                : "Your unsaved changes will be discarded."
          }
          confirmLabel={
            confirmation.kind === "approve"
              ? "Confirm approval"
              : confirmation.kind === "restore"
                ? "Restore"
                : "Discard edits"
          }
          onCancel={() => setConfirmation(null)}
          onConfirm={() => {
            const pending = confirmation;
            setConfirmation(null);
            if (pending.kind === "approve") void approve();
            else if (pending.kind === "restore") void restore();
            else if (pending.kind === "reload") void load();
            else if (pending.url) router.push(pending.url);
          }}
        />
      )}
    </div>
  );
}
