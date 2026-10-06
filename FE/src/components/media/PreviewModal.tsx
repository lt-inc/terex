"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
  fetchClipEditor,
  fetchClipPreview,
  fetchTwitchSourcePreview,
} from "@/lib/api";

export interface PreviewTarget {
  clipID: string;
  revisionID?: number;
  kind: "source" | "rendered";
  title: string;
  initialTime?: number;
  sourceID?: string;
  flowID?: string;
}

/** Mounted only after Play: list pages never request a video or signed URL. */
export default function PreviewModal({
  target,
  onClose,
  onTimeUpdate,
  onDuration,
}: {
  target: PreviewTarget | null;
  onClose: () => void;
  onTimeUpdate?: (seconds: number) => void;
  onDuration?: (seconds: number) => void;
}) {
  const video = useRef<HTMLVideoElement>(null);
  const close = useRef<HTMLButtonElement>(null);
  const dialog = useRef<HTMLElement>(null);
  const [url, setURL] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const refreshed = useRef(false);
  const position = useRef(0);
  const requestID = useRef(0);
  const resolvedRevision = useRef<number | undefined>(undefined);
  const closeCallback = useRef(onClose);
  closeCallback.current = onClose;
  const clipID = target?.clipID;
  const revisionID = target?.revisionID;
  const kind = target?.kind;
  const sourceID = target?.sourceID;
  const flowID = target?.flowID;

  const load = useCallback(async () => {
    if (!clipID || !kind) return;
    const attempt = ++requestID.current;
    setLoading(true);
    setError("");
    setURL("");
    try {
      if (!sourceID && !resolvedRevision.current) {
        const editor = await fetchClipEditor(clipID);
        if (attempt !== requestID.current) return;
        resolvedRevision.current = editor.revision_id;
      }
      const result =
        sourceID && flowID
          ? await fetchTwitchSourcePreview(sourceID, flowID)
          : await fetchClipPreview(clipID, kind, resolvedRevision.current);
      if (attempt !== requestID.current) return;
      if (!result.url)
        throw new Error(
          "Ссылка на видео отсутствует. Проверьте готовность файла.",
        );
      setURL(result.url);
    } catch (failure) {
      if (attempt === requestID.current)
        setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      if (attempt === requestID.current) setLoading(false);
    }
  }, [clipID, revisionID, kind, sourceID, flowID]);

  useEffect(() => {
    if (!clipID) return;
    const previous = document.activeElement as HTMLElement | null;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    close.current?.focus();
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") closeCallback.current();
      if (event.key === "Tab") {
        const elements = dialog.current?.querySelectorAll<HTMLElement>(
          'button:not(:disabled), a[href], video, [tabindex="0"]',
        );
        if (!elements?.length) return;
        const first = elements[0],
          last = elements[elements.length - 1];
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first.focus();
        }
      }
    };
    document.addEventListener("keydown", escape);
    return () => {
      document.body.style.overflow = previousOverflow;
      document.removeEventListener("keydown", escape);
      previous?.focus();
    };
  }, [clipID]);

  useEffect(() => {
    refreshed.current = false;
    resolvedRevision.current = revisionID;
    position.current = target?.initialTime ?? 0;
    setURL("");
    if (clipID) void load();
    const player = video.current;
    return () => {
      requestID.current++;
      if (player) {
        player.pause();
        player.removeAttribute("src");
        player.load();
      }
    };
  }, [clipID, revisionID, kind, load]);

  useEffect(() => {
    if (target?.initialTime == null) return;
    position.current = target.initialTime;
    if (video.current?.readyState && Number.isFinite(video.current.duration)) {
      video.current.currentTime = Math.min(
        position.current,
        video.current.duration,
      );
    }
  }, [target?.initialTime]);

  if (!target) return null;
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-3 sm:p-6"
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <section
        ref={dialog}
        role="dialog"
        aria-modal="true"
        aria-label={`Просмотр: ${target.title}`}
        className="flex max-h-[95vh] w-full max-w-5xl flex-col gap-3 overflow-auto rounded-xl border border-slate-600 bg-slate-900 p-4"
      >
        <div className="flex items-start justify-between gap-4">
          <div>
            <h2 className="break-words font-semibold">{target.title}</h2>
            <p className="mt-1 text-xs text-slate-400">
              {target.kind === "source" ? "Исходный файл" : "Готовая версия"}
              {target.revisionID ? ` · версия ${target.revisionID}` : ""}
            </p>
          </div>
          <button
            ref={close}
            type="button"
            onClick={onClose}
            className="shrink-0 rounded-lg bg-slate-700 px-3 py-2 text-sm"
          >
            Закрыть ✕
          </button>
        </div>
        {loading && (
          <p role="status" className="text-sm text-slate-400">
            Получаем ссылку на видео…
          </p>
        )}
        {error && (
          <div
            role="alert"
            className="rounded-lg border border-red-800 bg-red-950/30 p-3 text-sm text-red-300"
          >
            <p>{error}</p>
            <p className="mt-1 text-xs">
              Проверьте подключение и доступность медиа-хранилища. Ошибка
              просмотра не меняет состояние выпуска.
            </p>
            <button
              className="mt-2 rounded bg-slate-700 px-3 py-2"
              onClick={() => {
                refreshed.current = false;
                void load();
              }}
            >
              Попробовать снова
            </button>
          </div>
        )}
        <video
          ref={video}
          tabIndex={0}
          src={url || undefined}
          controls
          autoPlay
          playsInline
          preload="none"
          className="max-h-[75vh] w-full rounded-lg bg-black"
          onTimeUpdate={(event) => {
            if (loading || !url) return;
            position.current = event.currentTarget.currentTime;
            onTimeUpdate?.(position.current);
          }}
          onLoadedMetadata={(event) => {
            const player = event.currentTarget;
            if (Number.isFinite(player.duration)) {
              player.currentTime = Math.min(position.current, player.duration);
              onDuration?.(player.duration);
            }
          }}
          onError={() => {
            if (!url || loading) return;
            position.current = video.current?.currentTime || position.current;
            if (!refreshed.current) {
              refreshed.current = true;
              void load();
            } else
              setError(
                "Видео не удалось воспроизвести после обновления ссылки. Файл или медиа-хранилище недоступны либо браузер не поддерживает формат.",
              );
          }}
        />
      </section>
    </div>
  );
}
