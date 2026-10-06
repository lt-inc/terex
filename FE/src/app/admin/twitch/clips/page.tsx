"use client";
import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import TwitchNav from "@/components/twitch/TwitchNav";
import ConfirmAction from "@/components/ui/ConfirmAction";
import PreviewModal, {
  type PreviewTarget,
} from "@/components/media/PreviewModal";
import {
  actTwitchFlowClip,
  fetchTwitchFlowClips,
  fetchTwitchFlows,
  rejectImport,
} from "@/lib/api";
import { clipState, durationLabel } from "@/lib/twitch-status";
import type {
  TwitchFlow,
  TwitchFlowClip,
  TwitchFlowClipPage,
} from "@/types/twitch";
const input =
  "min-w-0 rounded-lg border border-slate-600 bg-slate-700 px-3 py-2 text-sm";
const button =
  "rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600 disabled:opacity-40";
export default function TwitchClipsPage() {
  const [flows, setFlows] = useState<TwitchFlow[]>([]),
    [flowID, setFlowID] = useState("");
  const [page, setPage] = useState<TwitchFlowClipPage>({
    items: [],
    total: 0,
    limit: 50,
    offset: 0,
  });
  const [search, setSearch] = useState(""),
    [q, setQ] = useState(""),
    [state, setState] = useState(""),
    [format, setFormat] = useState(""),
    [broadcaster, setBroadcaster] = useState(""),
    [offset, setOffset] = useState(0);
  const [busy, setBusy] = useState(""),
    [loading, setLoading] = useState(true),
    [error, setError] = useState(""),
    [notice, setNotice] = useState("");
  const [preview, setPreview] = useState<PreviewTarget | null>(null),
    [globalReject, setGlobalReject] = useState<TwitchFlowClip | null>(null);
  const requestID = useRef(0);
  useEffect(() => {
    let active = true;
    fetchTwitchFlows()
      .then((value) => {
        if (!active) return;
        setFlows(value || []);
        const selected = new URLSearchParams(window.location.search).get(
          "flow",
        );
        setFlowID(
          value?.some((flow) => flow.id === selected)
            ? selected!
            : value?.[0]?.id || "",
        );
        if (!value?.length) setLoading(false);
      })
      .catch((failure) => {
        if (active) {
          setError(
            failure instanceof Error ? failure.message : String(failure),
          );
          setLoading(false);
        }
      });
    return () => {
      active = false;
    };
  }, []);
  useEffect(() => {
    const timer = setTimeout(() => {
      setQ(search.trim());
      setOffset(0);
    }, 350);
    return () => clearTimeout(timer);
  }, [search]);
  const load = useCallback(
    async (quiet = false) => {
      if (!flowID) return;
      const attempt = ++requestID.current;
      if (!quiet) setLoading(true);
      try {
        const value = await fetchTwitchFlowClips(flowID, {
          offset: String(offset),
          q,
          state,
          format,
          broadcaster_id: broadcaster,
        });
        if (attempt === requestID.current) {
          setPage(value);
          setError("");
        }
      } catch (failure) {
        if (attempt === requestID.current)
          setError(
            failure instanceof Error ? failure.message : String(failure),
          );
      } finally {
        if (attempt === requestID.current) setLoading(false);
      }
    },
    [flowID, offset, q, state, format, broadcaster],
  );
  useEffect(() => {
    void load();
    return () => {
      requestID.current++;
    };
  }, [load]);
  useEffect(() => {
    const timer = setInterval(() => {
      if (document.visibilityState === "visible" && !busy) void load(true);
    }, 10000);
    return () => clearInterval(timer);
  }, [load, busy]);
  const selectedFlow = flows.find((flow) => flow.id === flowID);
  async function action(
    item: TwitchFlowClip,
    kind: "retry" | "exclude" | "global",
  ) {
    if (!item.import_id) return;
    setBusy(item.source.clip_id);
    setNotice("");
    setError("");
    try {
      if (kind === "global") await rejectImport(item.import_id);
      else
        await actTwitchFlowClip(flowID, item.import_id, kind, !item.excluded);
      setNotice(
        kind === "retry"
          ? "Повторная обработка запрошена."
          : kind === "global"
            ? "Исходник исключён из новых выпусков всех потоков. Уже начатые публикации проверьте в календаре."
            : item.excluded
              ? "Исходник возвращён в поток."
              : "Исходник пропущен только в этом потоке.",
      );
      await load(true);
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      setBusy("");
    }
  }
  return (
    <div className="flex flex-col gap-5 p-4 sm:p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">Клипы: состояние и выпуски</h1>
        <button
          className={button}
          disabled={loading || !!busy || !flowID}
          onClick={() => void load()}
        >
          Обновить
        </button>
      </div>
      <TwitchNav />
      <p className="text-sm text-slate-400">
        Исходник скачивается один раз. Shorts и подборки имеют собственные
        версии, одобрение и расписание. Ниже видно, что происходит и что
        требуется дальше.
      </p>
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
        <label className="flex flex-col gap-1 text-xs text-slate-400">
          Поток
          <select
            className={input}
            value={flowID}
            onChange={(event) => {
              setFlowID(event.target.value);
              setOffset(0);
              setBroadcaster("");
            }}
          >
            {!flows.length && <option value="">Нет потоков</option>}
            {flows.map((flow) => (
              <option key={flow.id} value={flow.id}>
                {flow.name}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-slate-400">
          Поиск
          <input
            className={input}
            placeholder="Название или стример"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-slate-400">
          Состояние исходника
          <select
            className={input}
            value={state}
            onChange={(event) => {
              setState(event.target.value);
              setOffset(0);
            }}
          >
            <option value="">Все состояния</option>
            {[
              "discovered",
              "waiting_quota",
              "queued",
              "downloading",
              "importing",
              "verifying",
              "ready",
              "failed",
              "retry_wait",
              "excluded",
              "rejected",
              "used",
            ].map((value) => (
              <option key={value} value={value}>
                {value === "used" ? "Использован" : clipState(value).label}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-slate-400">
          Формат выпуска
          <select
            className={input}
            value={format}
            onChange={(event) => {
              setFormat(event.target.value);
              setOffset(0);
            }}
          >
            <option value="">Все форматы</option>
            <option value="short">Shorts</option>
            <option value="compilation">Подборки</option>
          </select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-slate-400">
          Стример
          <select
            className={input}
            value={broadcaster}
            onChange={(event) => {
              setBroadcaster(event.target.value);
              setOffset(0);
            }}
          >
            <option value="">Все стримеры</option>
            {[
              ...new Map(
                page.items.map((item) => [
                  item.source.broadcaster_id,
                  item.source.broadcaster_login,
                ]),
              ).entries(),
            ].map(([id, name]) => (
              <option key={id} value={id}>
                {name || id}
              </option>
            ))}
            {broadcaster &&
              !page.items.some(
                (item) => item.source.broadcaster_id === broadcaster,
              ) && <option value={broadcaster}>Выбранный стример</option>}
          </select>
        </label>
      </div>
      {error && (
        <p
          role="alert"
          className="rounded-lg border border-red-800 p-3 text-sm text-red-300"
        >
          {error}
        </p>
      )}
      {notice && (
        <p role="status" className="text-sm text-emerald-300">
          {notice}
        </p>
      )}
      {loading && (
        <p role="status" className="text-sm text-slate-400">
          Загружаем метаданные…
        </p>
      )}
      {!loading && !page.items.length && (
        <div className="rounded-lg border border-slate-700 p-5 text-sm text-slate-400">
          {flows.length
            ? "Клипов по этим фильтрам пока нет. Проверьте активность потока, параметры поиска и общие лимиты collector."
            : "Сначала создайте поток и выберите Twitch-источники."}
          <Link
            href={
              flowID
                ? `/admin/twitch/${encodeURIComponent(flowID)}`
                : "/admin/twitch/new"
            }
            className="ml-2 text-indigo-400"
          >
            Настроить →
          </Link>
        </div>
      )}
      <div className="flex flex-col gap-4">
        {page.items.map((item) => {
          const source = item.source;
          const reasonState = item.reason?.code;
          const displayState = item.rejected
            ? "rejected"
            : item.excluded
              ? "excluded"
              : item.source_state === "discovered" &&
                  ["queued", "reserved", "retry_wait"].includes(
                    reasonState || "",
                  )
                ? reasonState
                : item.source_state === "downloading" &&
                    ["uploading", "verifying"].includes(reasonState || "")
                  ? "importing"
                  : item.source_state;
          const status = clipState(displayState);
          const next = item.reason?.next_attempt_at;
          const available =
            (["ready", "imported", "used"].includes(item.source_state) ||
              !!item.media_id) &&
            !!(item.source_clip_id || item.import_id);
          return (
            <article
              key={source.clip_id}
              className="flex flex-col gap-4 rounded-xl border border-slate-700 bg-slate-800 p-4 lg:flex-row"
            >
              <div className="w-full shrink-0 lg:w-56">
                <button
                  className="relative flex aspect-video w-full flex-col items-center justify-center overflow-hidden rounded-lg bg-slate-950 text-sm disabled:cursor-default"
                  disabled={!available}
                  onClick={() =>
                    setPreview({
                      clipID: item.source_clip_id || item.import_id,
                      sourceID: item.import_id,
                      flowID,
                      kind: "source",
                      title: source.title || source.clip_id,
                    })
                  }
                >
                  {source.thumbnail_url && (
                    <img
                      loading="lazy"
                      referrerPolicy="no-referrer"
                      className="absolute inset-0 h-full w-full object-cover opacity-70"
                      src={source.thumbnail_url}
                      alt=""
                    />
                  )}
                  <span className="relative rounded-lg bg-black/70 px-3 py-2">
                    {available ? "▶ Смотреть исходник" : "Файл пока не скачан"}
                  </span>
                </button>
                <a
                  className="mt-2 inline-block text-xs text-indigo-400"
                  href={source.url}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  Открыть исходный клип на Twitch ↗
                </a>
              </div>
              <div className="flex min-w-0 flex-1 flex-col gap-3">
                <div>
                  <h2 className="break-words font-semibold">
                    {source.title || source.clip_id}
                  </h2>
                  <p className="mt-1 text-xs text-slate-400">
                    {source.broadcaster_login} ·{" "}
                    {source.view_count.toLocaleString("ru")} просмотров ·{" "}
                    {durationLabel(source.duration_sec)} ·{" "}
                    {source.language || "язык неизвестен"}
                  </p>
                  <p className="mt-1 text-xs text-slate-500">
                    Создан:{" "}
                    {new Intl.DateTimeFormat("ru", {
                      timeZone: selectedFlow?.timezone || "Asia/Tomsk",
                      dateStyle: "short",
                      timeStyle: "short",
                    }).format(new Date(source.created_at))}
                    . Просмотры обновляются при проверке collector.
                  </p>
                </div>
                <section className="rounded-lg border border-slate-700 p-3">
                  <p
                    className={`text-sm font-medium ${item.source_state === "failed" ? "text-red-300" : "text-slate-200"}`}
                  >
                    Исходник: {status.label}
                  </p>
                  <p className="mt-1 text-xs text-slate-400">
                    {status.explanation}
                  </p>
                  {item.reason?.message && (
                    <p className="mt-2 text-sm text-amber-200">
                      {item.reason.message}
                    </p>
                  )}
                  {(item.reason?.started_at || item.reason?.attempt) && (
                    <p className="mt-2 text-xs text-slate-400">
                      {item.reason.started_at
                        ? `Обработка началась: ${new Intl.DateTimeFormat("ru", { timeZone: selectedFlow?.timezone || "Asia/Tomsk", dateStyle: "short", timeStyle: "short" }).format(new Date(item.reason.started_at))}`
                        : ""}
                      {item.reason.attempt
                        ? ` · попытка ${item.reason.attempt}`
                        : ""}
                    </p>
                  )}
                  {next && (
                    <p className="mt-1 text-xs text-slate-400">
                      Следующая попытка:{" "}
                      {new Intl.DateTimeFormat("ru", {
                        timeZone: selectedFlow?.timezone || "Asia/Tomsk",
                        dateStyle: "short",
                        timeStyle: "short",
                      }).format(new Date(next))}{" "}
                      · {selectedFlow?.timezone || "Asia/Tomsk"}
                    </p>
                  )}
                </section>
                <section>
                  <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-slate-400">
                    Выпуски этого потока
                  </h3>
                  {!item.outputs?.length ? (
                    <p className="text-xs text-slate-500">
                      Выпуск ещё не создан.{" "}
                      {item.rejected
                        ? "Исходник исключён из новых выпусков всех потоков."
                        : item.excluded
                          ? "Материал пропущен оператором."
                          : "Дождитесь готовности исходника и выполнения настроек формата."}
                    </p>
                  ) : (
                    <div className="flex flex-col gap-2">
                      {item.outputs.map((output) => {
                        const outputState =
                          output.publication_state ||
                          (output.render_state === "ready" &&
                          output.approval !== "approved"
                            ? "pending_approval"
                            : output.render_state);
                        return (
                          <div
                            key={`${output.clip_id}:${output.revision_id}`}
                            className="rounded-lg bg-slate-900/60 p-3 text-xs"
                          >
                            <div className="flex flex-wrap gap-2">
                              <span>
                                {output.format === "compilation"
                                  ? "Подборка"
                                  : "Short"}
                              </span>
                              <span className="text-indigo-300">
                                {clipState(outputState).label}
                              </span>
                              <span className="text-slate-500">
                                версия {output.revision_id}
                              </span>
                              {output.publish_at && (
                                <span>
                                  {new Intl.DateTimeFormat("ru", {
                                    timeZone:
                                      selectedFlow?.timezone || "Asia/Tomsk",
                                    dateStyle: "short",
                                    timeStyle: "short",
                                  }).format(new Date(output.publish_at))}
                                </span>
                              )}
                            </div>
                            <p className="mt-1 text-slate-400">
                              {clipState(outputState).explanation}
                            </p>
                            {output.error && (
                              <p className="mt-1 text-red-300">
                                {output.error}
                              </p>
                            )}
                            <div className="mt-2 flex gap-3">
                              {output.render_state === "ready" && (
                                <button
                                  className="text-indigo-400"
                                  onClick={() =>
                                    setPreview({
                                      clipID: output.clip_id,
                                      revisionID: output.revision_id,
                                      kind: "rendered",
                                      title: source.title || source.clip_id,
                                    })
                                  }
                                >
                                  ▶ Смотреть
                                </button>
                              )}
                              <Link
                                className="text-indigo-400"
                                href={`/admin/clips/${encodeURIComponent(output.clip_id)}`}
                              >
                                Открыть редактор →
                              </Link>
                            </div>
                          </div>
                        );
                      })}
                    </div>
                  )}
                </section>
                <div className="flex flex-wrap items-center gap-2">
                  {item.import_id &&
                    !item.rejected &&
                    !item.excluded &&
                    (item.reason?.actions?.includes("retry") ||
                      ["failed", "retry", "retry_wait"].includes(
                        item.source_state,
                      )) && (
                      <button
                        className={button}
                        disabled={!!busy}
                        onClick={() => void action(item, "retry")}
                      >
                        Повторить обработку
                      </button>
                    )}
                  {item.import_id && !item.rejected && (
                    <button
                      className={button}
                      disabled={!!busy}
                      onClick={() => void action(item, "exclude")}
                    >
                      {item.excluded
                        ? "Вернуть в поток"
                        : "Пропустить в этом потоке"}
                    </button>
                  )}
                  {item.import_id && !item.rejected && (
                    <button
                      className="px-2 py-2 text-xs text-red-300 disabled:opacity-40"
                      disabled={!!busy}
                      onClick={() => setGlobalReject(item)}
                    >
                      Исключить из всех потоков
                    </button>
                  )}
                </div>
              </div>
            </article>
          );
        })}
      </div>
      <div className="flex items-center justify-between gap-3 text-sm">
        <p className="text-slate-400">
          {page.total
            ? `${offset + 1}–${Math.min(offset + page.items.length, page.total)} из ${page.total}`
            : "0 клипов"}{" "}
          · 50 на странице
        </p>
        <div className="flex gap-2">
          <button
            className={button}
            disabled={offset === 0 || loading}
            onClick={() => setOffset(Math.max(0, offset - 50))}
          >
            ← Назад
          </button>
          <button
            className={button}
            disabled={offset + 50 >= page.total || loading}
            onClick={() => setOffset(offset + 50)}
          >
            Далее →
          </button>
        </div>
      </div>
      <PreviewModal target={preview} onClose={() => setPreview(null)} />
      {globalReject && (
        <ConfirmAction
          title="Исключить исходник везде"
          message="Материал перестанет попадать в новые выпуски всех потоков. Уже созданные и начатые публикации проверьте отдельно в календаре."
          confirmLabel="Исключить во всех потоках"
          onCancel={() => setGlobalReject(null)}
          onConfirm={() => {
            const item = globalReject;
            setGlobalReject(null);
            void action(item, "global");
          }}
        />
      )}
    </div>
  );
}
