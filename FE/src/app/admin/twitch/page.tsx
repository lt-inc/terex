"use client";
import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import TwitchNav from "@/components/twitch/TwitchNav";
import CapacityPanel from "@/components/twitch/CapacityPanel";
import ConfirmAction from "@/components/ui/ConfirmAction";
import {
  checkTwitchFlow,
  fetchAccountsList,
  fetchTwitchFlows,
  fetchTwitchStatus,
  saveTwitchFlow,
  archiveTwitchFlow,
  restoreTwitchFlow,
} from "@/lib/api";
import { durationLabel, queueAgeLabel } from "@/lib/twitch-status";
import { flowDestinations } from "@/lib/twitch-flow";
import type { TwitchCollectorStatus, TwitchFlow } from "@/types/twitch";
import type { AccountRecord } from "@/types";
const button =
  "rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600 disabled:opacity-40";
export default function TwitchFlowsPage() {
  const [flows, setFlows] = useState<TwitchFlow[]>([]),
    [accounts, setAccounts] = useState<AccountRecord[]>([]);
  const [status, setStatus] = useState<TwitchCollectorStatus | null>(null);
  const [includeArchived, setIncludeArchived] = useState(false);
  const [archiveTarget, setArchiveTarget] = useState<TwitchFlow | null>(null);
  const [loading, setLoading] = useState(true),
    [busy, setBusy] = useState(""),
    [error, setError] = useState(""),
    [notice, setNotice] = useState("");
  const requestID = useRef(0);
  const pending = useRef<AbortController | null>(null);
  const load = useCallback(async () => {
    pending.current?.abort();
    const controller = new AbortController();
    pending.current = controller;
    const attempt = ++requestID.current;
    setLoading(true);
    try {
      const labels = ["Потоки", "Аккаунты", "Collector"];
      const errors: string[] = ["", "", ""];
      const report = () => {
        if (attempt === requestID.current) setError(errors.filter(Boolean).join(" · "));
      };
      // Each request renders as soon as it arrives instead of waiting for the slowest one.
      const track = <T,>(index: number, request: Promise<T>, apply: (value: T) => void) =>
        request.then(
          (value) => {
            if (attempt !== requestID.current) return;
            apply(value);
            errors[index] = "";
            report();
          },
          (reason) => {
            if (attempt !== requestID.current) return;
            errors[index] = `${labels[index]}: ${reason instanceof Error ? reason.message : String(reason)}`;
            report();
          },
        );
      const flowsLoaded = track(0, fetchTwitchFlows(includeArchived, controller.signal), (value) => {
        setFlows(value ?? []);
        setLoading(false);
      });
      await Promise.all([
        flowsLoaded,
        track(1, fetchAccountsList(controller.signal), (value) => setAccounts(value ?? [])),
        track(2, fetchTwitchStatus(controller.signal), setStatus),
      ]);
    } catch (failure) {
      if (attempt === requestID.current) setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      if (attempt === requestID.current) {setLoading(false); pending.current = null;}
    }
  }, [includeArchived]);
  useEffect(() => {
    void load();
    return () => {requestID.current++; pending.current?.abort();};
  }, [load]);
  async function action(flow: TwitchFlow, kind: "check" | "pause" | "archive" | "restore") {
    setBusy(flow.id);
    setError("");
    setNotice("");
    try {
      if (kind === "check") {
        await checkTwitchFlow(flow.id);
        setNotice(
          `Проверка «${flow.name}» запрошена. Нажмите «Обновить», чтобы увидеть результат после завершения.`,
        );
      } else if (kind === "archive") {
        await archiveTwitchFlow(flow.id);
        setNotice("Поток архивирован. Новые задания остановлены, не начатые публикации удержаны. История и общие файлы сохранены до обычной очистки.");
      } else if (kind === "restore") {
        await restoreTwitchFlow(flow.id);
        setNotice("Поток восстановлен на паузе. Автопубликация выключена; прежние выпуски сами не возобновятся.");
      } else {
        await saveTwitchFlow({ ...flow, enabled: !flow.enabled });
        setNotice(
          flow.enabled
            ? "Поток приостановлен. Готовые материалы сохранены."
            : "Поток включён. Проверьте общую активность collector.",
        );
      }
      await load();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      setBusy("");
    }
  }
  return (
    <div className="flex flex-col gap-5 p-4 sm:p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">Потоки публикаций Twitch</h1>
        <div className="flex gap-2">
          <button
            className={button}
            disabled={loading || !!busy}
            onClick={() => void load()}
          >
            Обновить
          </button>
          <Link
            className="rounded-lg bg-indigo-600 px-4 py-2 text-sm hover:bg-indigo-500"
            href="/admin/twitch/new"
          >
            + Создать поток
          </Link>
        </div>
      </div>
      <TwitchNav />
      <p className="text-xs text-slate-400">Автообновление выключено. «Обновить» запрашивает текущие потоки и состояние collector; при ошибке предыдущие данные сохраняются. Готовность каждого потока обновляется своей кнопкой.</p>
      <label className="flex items-center gap-2 text-sm text-slate-300">
        <input type="checkbox" checked={includeArchived} onChange={(event) => setIncludeArchived(event.target.checked)} />
        Показать архивированные потоки
      </label>
      <p className="max-w-4xl text-sm text-slate-400">
        Выберите Twitch-каналы или категорию, язык и назначения YouTube/TikTok.
        Collector собирает исходники, Terex готовит выпуски. Проверка и
        публикация каждого потока управляются отдельно.
      </p>
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
      {status && (
        <div className="rounded-xl border border-slate-700 bg-slate-800 p-4 text-sm">
          <div className="flex flex-wrap items-center gap-3">
            <span
              className={
                status.credentials_ready ? "text-emerald-300" : "text-amber-300"
              }
            >
              {status.credentials_ready
                ? "Ключи Twitch настроены"
                : "Ключи Twitch отсутствуют"}
            </span>
            <span>
              {status.running
                ? "Идёт проверка"
                : status.paused
                  ? "Collector остановлен"
                  : "Collector работает по расписанию"}
            </span>
            <span className="text-slate-400">
              {status.waiting_assignment ?? "—"} ждёт назначения · {status.queued} разрешено к скачиванию · {status.downloading} скачивается ·{" "}
              {status.verification_pending ?? "—"} ждёт проверки · {status.verifying ?? "—"} проверяется · {status.imported} исходников готово
            </span>
            <Link href="/admin/twitch/settings" className="text-indigo-400">
              Общий бюджет →
            </Link>
          </div>
          <p className="mt-2 text-xs text-slate-400">Ожидание назначения не занимает исполнителя. Уже загруженные файлы проверяются отдельно от скачивания; полный backlog не мешает завершению импорта.</p>
          <p className="mt-1 text-xs text-slate-400">Самое старое разрешённое скачивание: {queueAgeLabel(status.oldest_authorized_at)}. Самая старая ожидающая проверка: {queueAgeLabel(status.oldest_verification_at)}.</p>
          {status.block_reason && (
            <p className="mt-2 text-amber-200">{status.block_reason}</p>
          )}
          {status.last_error && status.last_error !== status.block_reason && (
            <p className="mt-2 text-red-300">{status.last_error}</p>
          )}
        </div>
      )}
      {loading && <p className="text-slate-400">Загружаем потоки…</p>}
      {!loading && !flows.length && (
        <section className="rounded-xl border border-slate-700 bg-slate-800 p-6">
          <h2 className="font-semibold">Создайте первый поток</h2>
          <p className="mt-2 text-sm text-slate-400">
            Сначала подключите и подтвердите YouTube или TikTok в Accounts. Затем
            выберите Twitch-источники, включите поток и разрешите работу
            коллектора в настройках сервиса.
          </p>
          <Link
            className="mt-3 inline-block text-sm text-indigo-400"
            href="/admin/twitch/new"
          >
            Настроить поток →
          </Link>
        </section>
      )}
      <div className="grid gap-4 xl:grid-cols-2">
        {flows.map((flow) => {
          const timestamp = (value?: string) =>
            value
              ? new Intl.DateTimeFormat("ru", {
                  timeZone: flow.timezone,
                  dateStyle: "short",
                  timeStyle: "short",
                }).format(new Date(value))
              : "—";
          return (
            <article
              className="flex flex-col gap-3 rounded-xl border border-slate-700 bg-slate-800 p-5"
              key={flow.id}
            >
              <div className="flex items-start justify-between gap-3">
                <div>
                  {flow.archived ? <h2 className="text-lg font-semibold">{flow.name}</h2> :
                  <Link
                    href={`/admin/twitch/${encodeURIComponent(flow.id)}`}
                    className="text-lg font-semibold hover:text-indigo-300"
                  >
                    {flow.name}
                  </Link>
                  }
                  {flowDestinations(flow).map((destination) => {
                    const target = accounts.find((item) => item.id === destination.account_id);
                    return <p key={destination.provider} className="mt-1 break-words text-sm text-slate-400">{destination.provider === "tiktok" ? "TikTok" : "YouTube"}: {target?.remote_account_name || target?.name || destination.remote_account_id || "назначение недоступно"} · {destination.enabled ? destination.auto_publish ? "автопубликация" : "ручное расписание" : "выключено"} · {destination.daily_shorts}/сутки · {destination.schedule_horizon_hours} ч</p>;
                  })}
                </div>
                <span
                  className={`shrink-0 rounded px-2 py-1 text-xs ${flow.enabled ? "bg-emerald-950 text-emerald-300" : "bg-slate-700 text-slate-300"}`}
                >
                  {flow.archived ? "В архиве" : flow.enabled ? "Активен" : "На паузе"}
                </span>
              </div>
              <p className="text-sm">
                {flow.source_mode === "category"
                  ? `Категория: ${flow.category_name || flow.category_id}`
                  : `Twitch-каналы: ${(flow.channel_inputs || []).join(", ") || (flow.id === "default" && flow.legacy_length && !flow.broadcaster_ids.length ? "прежний список каналов" : `${flow.broadcaster_ids.length} источников`)}`}{" "}
                · {flow.language ? `язык ${flow.language}` : "любой язык"}
              </p>
              <p className="text-xs text-slate-400">
                Проверка каждые {flow.poll_interval_minutes} мин · от{" "}
                {flow.min_views} просмотров · за последние {flow.window_hours} ч
                {" "}· рейтинг: {flow.ranking_mode === "growth" ? "рост просмотров" : "просмотры"}
              </p>
              <p className="text-sm text-slate-300">
                {[
                  flow.shorts_enabled ? "Вертикальные клипы" : "",
                  flow.compilations_enabled
                    ? `Подборки ${flow.legacy_length ? "по старым правилам" : `${durationLabel(flow.compilation_min_duration_sec)}–${durationLabel(flow.compilation_max_duration_sec)}`}`
                    : "",
                ]
                  .filter(Boolean)
                  .join(" · ")}
              </p>
              <p
                className={
                  flowDestinations(flow).some((item) => item.enabled && item.auto_publish)
                    ? "text-sm text-amber-200"
                    : "text-sm text-slate-400"
                }
              >
                {flowDestinations(flow).some((item) => item.enabled && item.auto_publish)
                  ? "Автопубликация включена для выбранных назначений"
                  : "Время публикации назначается вручную"}{" "}
                · {flow.timezone}
              </p>
              <dl className="grid grid-cols-2 gap-3 text-xs">
                <div>
                  <dt className="text-slate-500">Последняя проверка</dt>
                  <dd className="mt-1">
                    {flow.last_checked_at
                      ? timestamp(flow.last_checked_at)
                      : "Данные доступны в состоянии клипов"}
                  </dd>
                </div>
                <div>
                  <dt className="text-slate-500">Ближайшая публикация</dt>
                  <dd className="mt-1">
                    {flow.next_publication_at
                      ? timestamp(flow.next_publication_at)
                      : "Смотрите календарь"}
                  </dd>
                </div>
                {flow.found_clips != null && (
                  <div>
                    <dt className="text-slate-500">Найдено клипов</dt>
                    <dd>{flow.found_clips}</dd>
                  </div>
                )}
                {flow.ready_outputs != null && (
                  <div>
                    <dt className="text-slate-500">Готовых выпусков</dt>
                    <dd>{flow.ready_outputs}</dd>
                  </div>
                )}
              </dl>
              {flow.block_reason && (
                <p className="text-sm text-amber-200">{flow.block_reason}</p>
              )}
              {flow.last_error && (
                <p className="text-sm text-red-300">{flow.last_error}</p>
              )}
              {!flow.archived && <CapacityPanel flowID={flow.id} timezone={flow.timezone} />}
              <p className="text-xs text-slate-400">Возраст исходников к выпуску: не более {flow.max_source_age_hours} ч от создания на Twitch. Сохранение файла не продлевает актуальность.</p>
              <div className="mt-auto flex flex-wrap gap-2 border-t border-slate-700 pt-3">
                <Link
                  className={button}
                  href={`/admin/twitch/clips?flow=${encodeURIComponent(flow.id)}`}
                >
                  Клипы и причины ожидания
                </Link>
                <Link
                  className={button}
                  href={`/admin/compilations?flow=${encodeURIComponent(flow.id)}`}
                >
                  Подборки
                </Link>
                <button
                  className={button}
                  disabled={
                    !!busy ||
                    !flow.enabled || flow.archived ||
                    status?.paused ||
                    !status?.credentials_ready
                  }
                  onClick={() => void action(flow, "check")}
                >
                  Проверить сейчас
                </button>
                <button
                  className={button}
                  disabled={!!busy}
                  onClick={() => void action(flow, flow.archived ? "restore" : "pause")}
                >
                  {flow.archived ? "Восстановить на паузе" : flow.enabled ? "Приостановить" : "Включить"}
                </button>
                {!flow.archived && <button className={`${button} text-amber-200`} disabled={!!busy} onClick={() => setArchiveTarget(flow)}>Архивировать</button>}
                {!flow.archived && <Link
                  className="self-center text-sm text-indigo-400"
                  href={`/admin/twitch/${encodeURIComponent(flow.id)}`}
                >
                  Настройки →
                </Link>}
              </div>
            </article>
          );
        })}
      </div>
      {archiveTarget && <ConfirmAction title={`Архивировать «${archiveTarget.name}»?`} message="Новые задания остановятся, не начавшиеся публикации будут удержаны. Начатая загрузка завершится. История сохранится; файлы удаляются отдельно по правилам хранения. Восстановление не включает поток и не возобновляет публикации." confirmLabel="Архивировать" onCancel={() => setArchiveTarget(null)} onConfirm={() => { const target = archiveTarget; setArchiveTarget(null); void action(target, "archive"); }} />}
    </div>
  );
}
