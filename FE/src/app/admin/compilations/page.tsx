"use client";
import Link from "next/link";
import DiagnosticsPanel from "@/components/twitch/DiagnosticsPanel";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  fetchCompilations,
  fetchTwitchFlows,
  triggerAutoCompilation,
} from "@/lib/api";
import type {
  AutoCompilationResult,
  Compilation,
  TwitchFlow,
} from "@/types/twitch";
import TwitchNav from "@/components/twitch/TwitchNav";
import PreviewModal, {
  type PreviewTarget,
} from "@/components/media/PreviewModal";
import { clipState, durationLabel } from "@/lib/twitch-status";
const button =
  "rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600 disabled:opacity-40";
const messageOf = (failure: unknown) =>
  failure instanceof Error ? failure.message : String(failure);
export default function CompilationsPage() {
  const [compilations, setCompilations] = useState<Compilation[]>([]),
    [flows, setFlows] = useState<TwitchFlow[]>([]),
    [flowID, setFlowID] = useState("");
  const [loading, setLoading] = useState(true),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [notice, setNotice] = useState("");
  const [automaticResult, setAutomaticResult] =
    useState<AutoCompilationResult | null>(null);
  const [preview, setPreview] = useState<PreviewTarget | null>(null);
  const requestID = useRef(0);
  const pending = useRef<AbortController | null>(null);
  useEffect(() => {
    let active = true;
    fetchTwitchFlows()
      .then((available) => {
        if (!active) return;
        setFlows(available || []);
        const requested = new URLSearchParams(window.location.search).get(
          "flow",
        );
        setFlowID(
          available.some((flow) => flow.id === requested)
            ? requested!
            : available[0]?.id || "",
        );
      })
      .catch((failure) => {
        if (active) setError(messageOf(failure));
      });
    return () => {
      active = false;
    };
  }, []);
  const load = useCallback(
    async (quiet = false) => {
      pending.current?.abort();
      const controller = new AbortController();
      pending.current = controller;
      const attempt = ++requestID.current;
      if (!quiet) setLoading(true);
      try {
        const value = await fetchCompilations(flowID, controller.signal);
        if (attempt === requestID.current) {
          setCompilations(value ?? []);
          setError("");
        }
      } catch (failure) {
        if (attempt === requestID.current) setError(messageOf(failure));
      } finally {
        if (attempt === requestID.current) {setLoading(false); pending.current = null;}
      }
    },
    [flowID],
  );
  useEffect(() => {
    void load();
    return () => {
      requestID.current++;
      pending.current?.abort();
    };
  }, [load]);
  async function automatic() {
    if (!flowID) return;
    setBusy(true);
    setError("");
    setNotice("");
    setAutomaticResult(null);
    try {
      const result = await triggerAutoCompilation(flowID);
      setAutomaticResult(result);
      if (result.compilation) {
        const compilation = result.compilation;
        setCompilations((previous) => [
          compilation,
          ...previous.filter((item) => item.clip_id !== compilation.clip_id),
        ]);
      }
      setNotice(
        result.message ||
          (result.created
            ? "Подборка создана и готовится к просмотру."
            : "Подборка не создана. Проверьте готовые исходники, время подготовки и сегодняшний черновик."),
      );
    } catch (failure) {
      setError(messageOf(failure));
    } finally {
      setBusy(false);
    }
  }
  const selectedFlow = flows.find((flow) => flow.id === flowID);
  return (
    <div className="flex min-h-full flex-col gap-5 p-4 sm:p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">Подборки</h1>
        <div className="flex flex-wrap gap-2">
          <button
            className={button}
            disabled={loading || busy}
            onClick={() => void load()}
          >
            Обновить
          </button>
          <button
            className={button}
            disabled={
              busy || loading || !flowID || !selectedFlow?.compilations_enabled
            }
            onClick={() => void automatic()}
          >
            {busy ? "Подбираем…" : "Подобрать автоматически"}
          </button>
          <Link
            href={`/admin/compilations/new${flowID ? `?flow=${encodeURIComponent(flowID)}` : ""}`}
            className="rounded-lg bg-indigo-600 px-3 py-2 text-sm hover:bg-indigo-500"
          >
            Собрать вручную
          </Link>
        </div>
      </div>
      <TwitchNav />
      <p className="text-xs text-slate-400">Автообновление выключено. Новые состояния подборок доступны по кнопке «Обновить». При ошибке последние загруженные данные сохраняются.</p>
      {flowID && <DiagnosticsPanel flowID={flowID} timezone={selectedFlow?.timezone} />}
      <label className="flex max-w-lg flex-col gap-1 text-xs text-slate-400">
        Поток
        <select
          className="rounded-lg border border-slate-600 bg-slate-700 px-3 py-2 text-sm"
          value={flowID}
          onChange={(event) => {
            setFlowID(event.target.value);
            setNotice("");
            setAutomaticResult(null);
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
      <p className="text-sm text-slate-400">
        Готовые исходники объединяются с секундными перебивками.{" "}
        {selectedFlow && !selectedFlow.legacy_length
          ? `Цель: ${durationLabel(selectedFlow.compilation_min_duration_sec)}–${durationLabel(selectedFlow.compilation_max_duration_sec)}, ${selectedFlow.compilation_min_clips}–${selectedFlow.compilation_max_clips} клипов. `
          : ""}
        {selectedFlow?.auto_publish
          ? "Новые автоматические подборки выпускаются по расписанию потока."
          : "Готовая версия допускается автоматически. Назначьте время вручную или включите автопубликацию потока."}
      </p>
      {error && (
        <p role="alert" className="text-sm text-red-400">
          {error}
        </p>
      )}
      {notice && (
        <div
          role="status"
          className={`rounded-lg border p-3 text-sm ${automaticResult?.created ? "border-emerald-800 bg-emerald-950/30 text-emerald-300" : "border-amber-800 bg-amber-950/30 text-amber-200"}`}
        >
          <p>{notice}</p>
          {automaticResult?.ready_duration_sec != null &&
            automaticResult.minimum_duration_sec != null && (
              <p className="mt-2">
                Готово {durationLabel(automaticResult.ready_duration_sec)} из
                необходимых{" "}
                {durationLabel(automaticResult.minimum_duration_sec)}.{" "}
                {automaticResult.missing_duration_sec
                  ? `Не хватает ${durationLabel(automaticResult.missing_duration_sec)}.`
                  : ""}
              </p>
            )}
          {automaticResult?.sources && <p className="mt-2 text-xs">Результат последнего запуска: готовых подходящих исходников {automaticResult.sources.eligible}. Актуальная готовность и причины ожидания показаны выше.</p>}
          {automaticResult && !automaticResult.created && (
            <div className="mt-2 flex flex-wrap gap-4">
              {automaticResult.compilation && (
                <Link
                  href={`/admin/compilations/${encodeURIComponent(automaticResult.compilation.clip_id)}`}
                  className="text-indigo-400"
                >
                  Открыть сегодняшнюю подборку →
                </Link>
              )}
              <Link
                href={`/admin/twitch/clips?flow=${encodeURIComponent(flowID)}`}
                className="text-indigo-400"
              >
                Состояние исходников →
              </Link>
              <Link
                href={`/admin/twitch/${encodeURIComponent(flowID)}`}
                className="text-indigo-400"
              >
                Настройки потока →
              </Link>
            </div>
          )}
        </div>
      )}
      {!loading && !compilations.length && (
        <p className="text-sm text-slate-500">
          Подборок пока нет. Подготовьте исходники или соберите черновик
          вручную.
        </p>
      )}
      <div className="grid gap-4 lg:grid-cols-2">
        {compilations.map((item) => (
          <article
            key={item.clip_id}
            className="min-w-0 rounded-xl border border-slate-700 bg-slate-800 p-4"
          >
            <h2 className="break-words font-semibold">
              {item.title || item.clip_id}
            </h2>
            <p className="mt-2 text-sm text-slate-400">
              {item.items?.length ?? 0} клипов ·{" "}
              {item.duration_sec != null
                ? `${durationLabel(item.duration_sec)} · `
                : ""}
              {clipState(item.state).label} · версия {item.version} ·{" "}
              {item.approved_at ? "Готова · допущена автоматически" : item.state === "ready" ? "Рендер готов · ожидает автоматических проверок" : "Ожидает готового рендера"}
            </p>
            <p className="mt-1 text-xs text-slate-500">
              {new Intl.DateTimeFormat("ru", {
                timeZone: selectedFlow?.timezone || "Asia/Tomsk",
                dateStyle: "short",
                timeStyle: "short",
              }).format(new Date(item.created_at))}{" "}
              · {selectedFlow?.timezone || "Asia/Tomsk"}
            </p>
            {item.error && (
              <p role="alert" className="mt-2 text-sm text-red-400">
                {item.error}
              </p>
            )}
            <div className="mt-4 flex flex-wrap gap-4 text-sm">
              {item.state === "ready" && (
                <button
                  className="text-indigo-400"
                  onClick={() =>
                    setPreview({
                      clipID: item.clip_id,
                      revisionID: item.revision_id,
                      kind: "rendered",
                      title: item.title || item.clip_id,
                    })
                  }
                >
                  ▶ Смотреть
                </button>
              )}
              <Link
                href={`/admin/compilations/${encodeURIComponent(item.clip_id)}`}
                className="text-indigo-400"
              >
                Изменить состав →
              </Link>
              <Link
                href={`/admin/clips/${encodeURIComponent(item.clip_id)}`}
                className="text-indigo-400"
              >
                Редактор и расписание →
              </Link>
            </div>
          </article>
        ))}
      </div>
      <PreviewModal target={preview} onClose={() => setPreview(null)} />
    </div>
  );
}
