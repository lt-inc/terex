"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import ConfirmAction from "@/components/ui/ConfirmAction";
import {
  createCompilation,
  fetchCompilation,
  fetchImports,
  fetchTwitchFlows,
  saveCompilation,
} from "@/lib/api";
import {
  eligibleImports,
  moveCompilationItem,
  validateCompilation,
} from "@/lib/twitch";
import type {
  Compilation,
  CompilationItem,
  ImportedClip,
  TwitchFlow,
} from "@/types/twitch";
import TwitchNav from "@/components/twitch/TwitchNav";
import PreviewModal, {
  type PreviewTarget,
} from "@/components/media/PreviewModal";
import { durationLabel } from "@/lib/twitch-status";

const input =
  "w-full min-w-0 rounded-md border border-slate-600 bg-slate-700 px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500";
const button =
  "rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600 disabled:opacity-40";
const panel = "rounded-xl border border-slate-700 bg-slate-800 p-4 sm:p-5";
const messageOf = (failure: unknown) =>
  failure instanceof Error ? failure.message : String(failure);
const signature = (title: string, items: CompilationItem[]) =>
  JSON.stringify({ title, items });

export default function CompilationBuilder({ clipID }: { clipID?: string }) {
  const router = useRouter();
  const [saved, setSaved] = useState<Compilation | null>(null);
  const [title, setTitle] = useState("");
  const [items, setItems] = useState<CompilationItem[]>([]);
  const [imports, setImports] = useState<ImportedClip[]>([]);
  const [settings, setSettings] = useState<TwitchFlow | null>(null);
  const [flows, setFlows] = useState<TwitchFlow[]>([]);
  const [flowID, setFlowID] = useState("");
  const [preview, setPreview] = useState<PreviewTarget | null>(null);
  const requestID = useRef(0);
  const pending = useRef<AbortController | null>(null);
  const selectedFlow = useRef("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [candidateID, setCandidateID] = useState("");
  const [discardAction, setDiscardAction] = useState<"reload" | "leave" | null>(
    null,
  );
  const eligible = useMemo(() => eligibleImports(imports), [imports]);
  const sources = useMemo(
    () =>
      new Map(
        imports.flatMap(
          (item) =>
            [
              [item.import_id, item],
              [item.clip_id || item.import_id, item],
            ] as [string, ImportedClip][],
        ),
      ),
    [imports],
  );
  const candidates = eligible.filter(
    (item) =>
      !items.some(
        (selected) =>
          (selected.source_id || selected.clip_id) ===
          (selected.source_id ? item.import_id : item.clip_id),
      ),
  );
  const dirty = saved
    ? signature(title, items) !== signature(saved.title, saved.items)
    : !!title || items.length > 0;
  const duration = (source?: ImportedClip) =>
    source?.duration_sec || source?.source.duration_sec || 0;
  const totalDuration =
    items.reduce(
      (sum, item) =>
        sum + Math.ceil((item.end_sec - item.start_sec) * 30 - 1e-7) / 30,
      0,
    ) + Math.max(0, items.length - 1);
  const channelDurations = items.reduce<Record<string, number>>(
    (result, item) => {
      const channel =
        sources.get(item.source_id || item.clip_id)?.source.broadcaster_login ||
        "Неизвестный стример";
      result[channel] = (result[channel] || 0) + item.end_sec - item.start_sec;
      return result;
    },
    {},
  );

  const load = useCallback(async () => {
    pending.current?.abort();
    const controller = new AbortController();
    pending.current = controller;
    const attempt = ++requestID.current;
    setLoading(true);
    setError("");
    try {
      const [availableFlows, existing] = await Promise.all([
        fetchTwitchFlows(false, controller.signal),
        clipID ? fetchCompilation(clipID, controller.signal) : Promise.resolve(null),
      ]);
      const selectedID =
        existing?.flow_id ||
        selectedFlow.current ||
        new URLSearchParams(window.location.search).get("flow") ||
        availableFlows?.[0]?.id ||
        "";
      const available = await fetchImports("ready", selectedID, controller.signal);
      if (attempt !== requestID.current) return;
      setFlows(availableFlows || []);
      selectedFlow.current = selectedID;
      setFlowID(selectedID);
      setImports(available ?? []);
      setSettings(
        existing?.flow_config ||
          availableFlows.find((flow) => flow.id === selectedID) ||
          null,
      );
      setSaved(existing);
      if (existing) {
        setTitle(existing.title);
        setItems(existing.items ?? []);
      } else {
        setTitle("");
        setItems([]);
      }
    } catch (failure) {
      if (attempt === requestID.current) setError(messageOf(failure));
    } finally {
      if (attempt === requestID.current) {setLoading(false); pending.current = null;}
    }
  }, [clipID]);
  useEffect(() => {
    void load();
    return () => {
      requestID.current++;
      pending.current?.abort();
    };
  }, [load]);
  useEffect(() => {
    if (!dirty) return;
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault();
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  function add() {
    if (busy || loading || pending.current || !settings) return;
    const source = candidates.find((item) => item.import_id === candidateID);
    if (!source || duration(source) <= 0) return;
    setItems((previous) => [
      ...previous,
      {
        clip_id: source.clip_id || "",
        revision_id: source.revision_id || 0,
        source_id: source.import_id,
        start_sec: 0,
        end_sec: duration(source),
      },
    ]);
    setCandidateID("");
  }
  async function save() {
    if (!settings || busy || loading || pending.current || settings.id !== flowID) return;
    setError("");
    setNotice("");
    const issue = validateCompilation(
      title,
      items,
      settings.compilation_min_clips,
      settings.compilation_max_clips,
    );
    if (issue) {
      setError(issue);
      return;
    }
    if (
      items.some((item) => {
        const source = eligible.find((candidate) =>
          item.source_id
            ? candidate.import_id === item.source_id
            : candidate.clip_id === item.clip_id,
        );
        return !source || item.end_sec > duration(source);
      })
    ) {
      setError(
        "Один из исходников больше не готов, исключён или не покрывает выбранные границы. Обновите список исходников перед сохранением.",
      );
      return;
    }
    setBusy(true);
    try {
      const spec = { title: title.trim(), items, flow_id: flowID };
      const result =
        clipID && saved
          ? await saveCompilation(clipID, saved.version, spec)
          : await createCompilation(spec);
      setSaved(result);
      setTitle(result.title);
      setItems(result.items ?? []);
      setNotice(
        "Подборка отправлена на рендер. Готовая версия допускается автоматически; публикация зависит от настроек потока и расписания.",
      );
      if (!clipID)
        router.replace(
          `/admin/compilations/${encodeURIComponent(result.clip_id)}`,
        );
    } catch (failure) {
      setError(messageOf(failure));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex min-h-full flex-col gap-5 p-4 sm:p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">
          {clipID ? "Редактировать подборку" : "Новая подборка"}
        </h1>
        <Link
          href="/admin/compilations"
          className="text-sm text-indigo-400"
          onClick={(event) => {
            if (dirty) {
              event.preventDefault();
              setDiscardAction("leave");
            }
          }}
        >
          Все подборки →
        </Link>
      </div>
      <TwitchNav />
      <p className="text-sm text-slate-400">
        Выберите готовые исходники потока, порядок и границы. Между клипами
        добавляется секундная перебивка. Изменение состава создаёт новую версию
        и удерживает будущий выпуск до готовности нового рендера.
      </p>
      <p className="text-xs text-slate-400">Автообновление выключено. После рендера нажмите «Обновить». При ошибке ваши правки и предыдущие данные сохраняются.</p>
      {error && (
        <p role="alert" className="text-sm text-red-400">
          {error}
        </p>
      )}
      {notice && (
        <p role="status" className="text-sm text-emerald-400">
          {notice}
        </p>
      )}
      {loading && (
        <p role="status" className="text-sm text-slate-400">
          Загружаем исходники и подборку…
        </p>
      )}
      <section className={panel} aria-labelledby="compilation-title">
        <form
          className="flex flex-col gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            void save();
          }}
        >
          <fieldset disabled={busy || loading} className="contents">
          <label className="flex flex-col gap-1 text-xs text-slate-400">
            Поток
            <select
              className={input}
              value={flowID}
              disabled={!!clipID || busy || loading || dirty}
              onChange={(event) => {selectedFlow.current = event.target.value; setFlowID(event.target.value); setImports([]); setSettings(null); setSaved(null); setTitle(""); setItems([]); setCandidateID(""); setPreview(null); void load();}}
            >
              {flows.map((flow) => (
                <option key={flow.id} value={flow.id}>
                  {flow.name}
                </option>
              ))}
            </select>
          </label>
          <label
            id="compilation-title"
            className="flex flex-col gap-1 text-xs text-slate-400"
          >
            Название подборки
            <input
              className={input}
              required
              maxLength={100}
              value={title}
              disabled={loading || busy || !settings}
              onChange={(event) => setTitle(event.target.value)}
            />
          </label>
          <div className="flex flex-col gap-2 sm:flex-row">
            <select
              aria-label="Готовый исходник для добавления"
              className={input}
              value={candidateID}
              disabled={loading || busy || !settings}
              onChange={(event) => setCandidateID(event.target.value)}
            >
              <option value="">Выберите готовый исходник</option>
              {candidates.map((source) => (
                <option key={source.import_id} value={source.import_id}>
                  {source.source.broadcaster_login} ·{" "}
                  {source.source.title || source.source.clip_id} ·{" "}
                  {duration(source).toFixed(1)}s
                </option>
              ))}
            </select>
            <button
              type="button"
              className={`${button} shrink-0`}
              disabled={
                !candidateID ||
                busy ||
                items.length >= (settings?.compilation_max_clips ?? 60)
              }
              onClick={add}
            >
              Добавить клип
            </button>
          </div>
          <p className="text-xs text-slate-400">
            {settings
              ? `${settings.compilation_min_clips}–${settings.compilation_max_clips} клипов. `
              : ""}
            Используются проверенные исходники, а не вертикальные Shorts.
          </p>
          <section className="rounded-lg border border-slate-700 p-3">
            <p className="text-sm font-semibold">
              Общая длина: {durationLabel(totalDuration)}
              {settings && !settings.legacy_length
                ? ` · цель ${durationLabel(settings.compilation_min_duration_sec)}–${durationLabel(settings.compilation_max_duration_sec)}`
                : ""}
            </p>
            {settings &&
              !settings.legacy_length &&
              totalDuration < settings.compilation_min_duration_sec && (
                <p className="mt-1 text-xs text-amber-200">
                  До минимальной длины не хватает{" "}
                  {durationLabel(
                    settings.compilation_min_duration_sec - totalDuration,
                  )}
                  . Автоматическая подборка ждёт достаточного материала.
                </p>
              )}
            <p className="mt-1 text-xs text-slate-400">
              С учётом {Math.max(0, items.length - 1)} секунд перебивок.{" "}
              {Object.entries(channelDurations)
                .map(
                  ([channel, seconds]) =>
                    `${channel}: ${durationLabel(seconds)}`,
                )
                .join(" · ")}
            </p>
          </section>
          {!items.length && (
            <p className="text-sm text-slate-500">Добавьте клипы в подборку.</p>
          )}
          <ol className="flex flex-col gap-3">
            {items.map((item, index) => {
              const source = sources.get(item.source_id || item.clip_id);
              return (
                <li
                  key={item.source_id || item.clip_id}
                  className="rounded-lg border border-slate-700 p-3"
                >
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="min-w-0">
                      <p className="break-words text-sm font-medium">
                        {index + 1}.{" "}
                        {source?.source.title || item.source_id || item.clip_id}
                      </p>
                      <p className="mt-1 text-xs text-slate-400">
                        {source?.source.broadcaster_login ||
                          "Исходник недоступен"}{" "}
                        · {durationLabel(item.end_sec - item.start_sec)}
                      </p>
                      {source && (
                        <button
                          type="button"
                          className="mt-1 text-xs text-indigo-400"
                          onClick={() =>
                            setPreview({
                              clipID: source.import_id,
                              sourceID: source.import_id,
                              flowID,
                              kind: "source",
                              title:
                                source.source.title || source.source.clip_id,
                            })
                          }
                        >
                          ▶ Смотреть исходник
                        </button>
                      )}
                    </div>
                    <div className="flex gap-2">
                      <button
                        type="button"
                        className={button}
                        aria-label={`Переместить клип ${index + 1} выше`}
                        disabled={busy || index === 0}
                        onClick={() =>
                          setItems((previous) =>
                            moveCompilationItem(previous, index, -1),
                          )
                        }
                      >
                        ↑
                      </button>
                      <button
                        type="button"
                        className={button}
                        aria-label={`Переместить клип ${index + 1} ниже`}
                        disabled={busy || index === items.length - 1}
                        onClick={() =>
                          setItems((previous) =>
                            moveCompilationItem(previous, index, 1),
                          )
                        }
                      >
                        ↓
                      </button>
                      <button
                        type="button"
                        className={`${button} text-red-300`}
                        aria-label={`Удалить клип ${index + 1}`}
                        disabled={busy}
                        onClick={() =>
                          setItems((previous) =>
                            previous.filter(
                              (_, selected) => selected !== index,
                            ),
                          )
                        }
                      >
                        Убрать
                      </button>
                    </div>
                  </div>
                  <div className="mt-3 grid gap-3 sm:grid-cols-2">
                    {(["start_sec", "end_sec"] as const).map((bound) => (
                      <label
                        key={bound}
                        className="flex flex-col gap-1 text-xs text-slate-400"
                      >
                        {bound === "start_sec"
                          ? "Начало, секунд"
                          : "Конец, секунд"}
                        <input
                          className={input}
                          type="number"
                          required
                          min={0}
                          max={source ? duration(source) : undefined}
                          step="any"
                          disabled={busy}
                          value={
                            Number.isFinite(item[bound]) ? item[bound] : ""
                          }
                          onChange={(event) => {
                            const value =
                              event.target.value === ""
                                ? NaN
                                : Number(event.target.value);
                            setItems((previous) =>
                              previous.map((selected, selectedIndex) =>
                                selectedIndex === index
                                  ? { ...selected, [bound]: value }
                                  : selected,
                              ),
                            );
                          }}
                        />
                      </label>
                    ))}
                  </div>
                </li>
              );
            })}
          </ol>
          <div className="flex flex-wrap items-center gap-3">
            <button
              className="rounded-lg bg-indigo-600 px-4 py-2 text-sm hover:bg-indigo-500 disabled:opacity-40"
              disabled={
                busy ||
                loading ||
                !settings ||
                (clipID ? !saved || !dirty : false)
              }
            >
              {busy
                ? "Сохраняем…"
                : clipID
                  ? "Сохранить и собрать"
                  : "Создать и собрать"}
            </button>
            <button
              type="button"
              className={button}
              disabled={busy || loading}
              onClick={() => {
                if (dirty) setDiscardAction("reload");
                else void load();
              }}
            >
              Обновить
            </button>
            {dirty && (
              <span className="text-xs text-amber-300">
                Есть несохранённые правки
              </span>
            )}
          </div>
          </fieldset>
        </form>
      </section>
      {saved && (
        <section className={panel} aria-labelledby="preview-title">
          <h2 id="preview-title" className="font-semibold">
            Просмотр и проверка
          </h2>
          <p className="mt-2 text-sm text-slate-400">
            Обработка: {saved.state} · версия {saved.version} ·{" "}
            {saved.approved_at ? "Готова · допущена автоматически" : saved.state === "ready" ? "Рендер готов · ожидает автоматических проверок" : "Ожидает готового рендера"}
          </p>
          {saved.error && (
            <p role="alert" className="mt-2 text-sm text-red-400">
              {saved.error}
            </p>
          )}
          {saved.state === "ready" ? (
            <button
              type="button"
              className={`${button} mt-4`}
              onClick={() =>
                setPreview({
                  clipID: saved.clip_id,
                  revisionID: saved.revision_id,
                  kind: "rendered",
                  title: saved.title,
                })
              }
            >
              ▶ Смотреть готовую подборку
            </button>
          ) : (
            <p className="mt-3 text-sm text-slate-500">
              Просмотр станет доступен после рендера.
            </p>
          )}
          <div className="mt-4 flex flex-wrap gap-4 text-sm">
            <Link
              href={`/admin/clips/${encodeURIComponent(saved.clip_id)}`}
              className="text-indigo-400"
            >
              Открыть редактор и расписание →
            </Link>
            <Link href="/admin/calendar" className="text-indigo-400">
              Календарь публикаций →
            </Link>
          </div>
          <p className="mt-3 text-xs text-slate-400">
            Изменение состава создаёт новый рендер. Готовая версия допускается автоматически; будущая публикация ждёт окончания обработки.
          </p>
        </section>
      )}
      <PreviewModal target={preview} onClose={() => setPreview(null)} />
      {discardAction && (
        <ConfirmAction
          title="Отбросить несохранённые правки"
          message="Название, порядок и границы клипов пока не сохранены. Отбросить изменения?"
          confirmLabel="Отбросить"
          onCancel={() => setDiscardAction(null)}
          onConfirm={() => {
            const next = discardAction;
            setDiscardAction(null);
            if (next === "reload") void load();
            else router.push("/admin/compilations");
          }}
        />
      )}
    </div>
  );
}
