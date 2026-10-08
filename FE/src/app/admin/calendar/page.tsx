"use client";
import { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import ConfirmAction from "@/components/ui/ConfirmAction";
import {
  actPublication,
  ApiError,
  fetchAccountsList,
  fetchPublicationHistory,
  fetchPublications,
  updatePublication,
  fetchTwitchPolicyPreview,
} from "@/lib/api";
import {
  addDays,
  shiftMonth,
  calendarDays,
  DEFAULT_TIMEZONE,
  formatInZone,
  TIMEZONES,
  zonedParts,
  zonePartsToISO,
} from "@/lib/datetime";
import type {
  AccountRecord,
  PublicationEvent,
  PublicationHistory,
} from "@/types";
import type { TwitchPolicyPreview } from "@/types/twitch";
const cls =
  "bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 [color-scheme:dark]";
const button =
  "bg-slate-700 hover:bg-slate-600 disabled:opacity-40 rounded-lg px-3 py-2 text-sm";
const statuses = [
  "scheduled",
  "queued",
  "on_hold",
  "needs_attention",
  "blocked",
  "uploading",
  "processing",
  "publishing",
  "published",
  "cancelled",
  "cancel_pending",
  "retry_wait",
  "failed",
];
function isStarted(event: PublicationEvent) {
  return (
    !!event.started_at ||
    [
      "sent",
      "uploading",
      "uploaded",
      "processing",
      "publishing",
      "published",
    ].includes(event.state)
  );
}
function color(state: string) {
  return state === "published"
    ? "border-emerald-700 bg-emerald-950/40"
    : state === "failed"
      ? "border-red-700 bg-red-950/40"
      : state.includes("hold") || state === "held"
        ? "border-amber-700 bg-amber-950/40"
        : "border-indigo-700 bg-indigo-950/40";
}
export default function CalendarPage() {
  const [view, setView] = useState<"week" | "month">("week"),
    [zone, setZone] = useState(DEFAULT_TIMEZONE),
    [date, setDate] = useState(() => zonedParts(new Date().toISOString()).date),
    [account, setAccount] = useState(""),
    [platform, setPlatform] = useState(""),
    [state, setState] = useState("");
  const [accounts, setAccounts] = useState<AccountRecord[]>([]),
    [events, setEvents] = useState<PublicationEvent[]>([]),
    [selected, setSelected] = useState<PublicationEvent | null>(null),
    [loading, setLoading] = useState(false),
    [error, setError] = useState(""),
    [refresh, setRefresh] = useState(0);
  const days = useMemo(() => calendarDays(date, view), [date, view]);
  useEffect(() => {
    fetchAccountsList()
      .then(setAccounts)
      .catch((e) => setError(e.message));
  }, []);
  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    let from: string, to: string;
    try {
      from = zonePartsToISO(days[0], "00:00", zone);
      to = zonePartsToISO(addDays(days[days.length - 1], 1), "00:00", zone);
    } catch (e) {
      setError(String(e));
      setLoading(false);
      return;
    }
    fetchPublications({
      from,
      to,
      ...(account ? { account_id: account } : {}),
      ...(platform ? { platform } : {}),
      ...(state ? { status: state } : {}),
      limit: "1000",
    })
      .then((rows) => {
        if (active) setEvents(rows);
      })
      .catch((e) => {
        if (active) setError(e.message);
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [days, zone, account, platform, state, refresh]);
  useEffect(() => {
    const timer = setInterval(() => setRefresh((v) => v + 1), 15000);
    return () => clearInterval(timer);
  }, []);
  const groups = useMemo(() => {
    const map = new Map<string, PublicationEvent[]>();
    for (const e of events) {
      const key = zonedParts(e.publish_at, zone).date;
      map.set(key, [...(map.get(key) ?? []), e]);
    }
    return map;
  }, [events, zone]);
  function changed(row: PublicationEvent) {
    setEvents((prev) => prev.map((e) => (e.id === row.id ? row : e)));
    setSelected(row);
    setRefresh((v) => v + 1);
  }
  return (
    <div className="p-6 flex flex-col gap-5">
      <div className="flex items-center justify-between gap-3 flex-wrap">
        <h1 className="text-xl font-semibold">Publication calendar</h1>
        <Link href="/admin/clips" className="text-sm text-indigo-400">
          Review clips →
        </Link>
      </div>
      <p className="text-sm text-slate-400">
        Times show when upload starts. Processing and public visibility are
        tracked separately. Overdue approved events catch up automatically;
        older backlog requires confirmation.
      </p>
      <div className="flex flex-wrap gap-2 items-center">
        <button
          className={button}
          onClick={() =>
            setDate(view === "week" ? addDays(date, -7) : shiftMonth(date, -1))
          }
        >
          ←
        </button>
        <button
          className={button}
          onClick={() =>
            setDate(zonedParts(new Date().toISOString(), zone).date)
          }
        >
          Today
        </button>
        <button
          className={button}
          onClick={() =>
            setDate(view === "week" ? addDays(date, 7) : shiftMonth(date, 1))
          }
        >
          →
        </button>
        <input
          aria-label="Calendar date"
          type="date"
          className={cls}
          value={date}
          onChange={(e) => e.target.value && setDate(e.target.value)}
        />
        <select
          aria-label="Calendar view"
          className={cls}
          value={view}
          onChange={(e) => setView(e.target.value as "week" | "month")}
        >
          <option value="week">Week</option>
          <option value="month">Month</option>
        </select>
        <select
          aria-label="Timezone"
          className={cls}
          value={zone}
          onChange={(e) => setZone(e.target.value)}
        >
          {TIMEZONES.map((z) => (
            <option key={z}>{z}</option>
          ))}
        </select>
        <button
          className={button}
          disabled={loading}
          onClick={() => setRefresh((v) => v + 1)}
        >
          {loading ? "Refreshing…" : "Refresh"}
        </button>
      </div>
      <div className="flex flex-wrap gap-2">
        <select
          aria-label="Account filter"
          className={cls}
          value={account}
          onChange={(e) => setAccount(e.target.value)}
        >
          <option value="">All accounts</option>
          {accounts.map((a) => (
            <option key={a.id} value={a.id}>
              {a.name} · {a.platform}
            </option>
          ))}
        </select>
        <select
          aria-label="Provider filter"
          className={cls}
          value={platform}
          onChange={(e) => setPlatform(e.target.value)}
        >
          <option value="">All providers</option>
          {["youtube", "tiktok", "instagram"].map((p) => (
            <option key={p}>{p}</option>
          ))}
        </select>
        <select
          aria-label="State filter"
          className={cls}
          value={state}
          onChange={(e) => setState(e.target.value)}
        >
          <option value="">All states</option>
          {statuses.map((s) => (
            <option key={s}>{s}</option>
          ))}
        </select>
        <span className="text-slate-500 text-xs self-center">
          {events.length} events
        </span>
      </div>
      {error && (
        <p role="alert" className="text-red-400">
          {error}
        </p>
      )}
      <div className="overflow-x-auto">
        <div className="grid grid-cols-7 min-w-[800px] gap-2">
          {["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"].map((d) => (
            <p key={d} className="text-xs text-slate-500 px-2">
              {d}
            </p>
          ))}
          {days.map((day) => (
            <div
              key={day}
              className={`min-h-40 border rounded-xl p-2 flex flex-col gap-2 ${day === zonedParts(new Date().toISOString(), zone).date ? "border-indigo-500 bg-slate-800" : "border-slate-700 bg-slate-800/50"}`}
            >
              <p className="text-xs text-slate-400">
                {day.slice(8)}.{day.slice(5, 7)}
              </p>
              {(groups.get(day) ?? []).map((event) => (
                <button
                  key={event.id}
                  onClick={() => setSelected(event)}
                  className={`border rounded-md p-2 text-left flex flex-col gap-1 ${color(event.state)}`}
                >
                  <span className="text-xs font-medium">
                    {zonedParts(event.publish_at, zone).time} ·{" "}
                    {event.account_name}
                  </span>
                  <span className="text-xs truncate">
                    {event.title || event.clip_id}
                  </span>
                  <span className="text-[10px] text-slate-400">
                    {event.state} · {event.platform}
                  </span>
                </button>
              ))}
            </div>
          ))}
        </div>
      </div>
      {selected && (
        <EventModal
          key={selected.id}
          event={selected}
          zone={zone}
          onClose={() => setSelected(null)}
          onChanged={changed}
        />
      )}
    </div>
  );
}
function EventModal({
  event,
  zone,
  onClose,
  onChanged,
}: {
  event: PublicationEvent;
  zone: string;
  onClose: () => void;
  onChanged: (e: PublicationEvent) => void;
}) {
  const [confirmation, setConfirmation] = useState<
    "approve" | "retry" | "cancel" | "publish-now" | null
  >(null);
  const [freshnessOverride, setFreshnessOverride] = useState(false);
  const [policyViolation, setPolicyViolation] = useState<TwitchPolicyPreview["violations"][number] | null>(null);
  const twitchPublication = !!event.twitch_policy || event.clip_id.startsWith("twitch-") || event.clip_id.startsWith("compilation-");
  const [value, setValue] = useState(() => {
      const p = zonedParts(event.publish_at, zone);
      return `${p.date}T${p.time}`;
    }),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [history, setHistory] = useState<PublicationHistory[]>([]);
  const loadHistory = useCallback(
    () =>
      fetchPublicationHistory(event.id)
        .then(setHistory)
        .catch((e) => setError(e.message)),
    [event.id],
  );
  useEffect(() => {
    loadHistory();
  }, [loadHistory, event.version]);
  useEffect(() => {
    setFreshnessOverride(false);
  }, [value, event.version, event.revision_id]);
  useEffect(() => {
    if (!twitchPublication) return;
    let active = true;
    fetchTwitchPolicyPreview().then((result) => {
      if (active) setPolicyViolation((result.violations || []).find((item) => item.publication_id === event.id) || null);
    }).catch(() => {});
    return () => { active = false; };
  }, [event.id, event.version, twitchPublication]);
  useEffect(() => {
    function key(e: KeyboardEvent) {
      if (e.key === "Escape" && !busy) onClose();
    }
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [onClose, busy]);
  let selectedAt = event.publish_at;
  try { const [date, time] = value.split("T"); selectedAt = zonePartsToISO(date, time, zone); } catch { /* The server validates incomplete dates. */ }
  const selectedAge = event.twitch_policy?.source_created_at ? Math.max(0, Math.round((new Date(selectedAt).getTime() - new Date(event.twitch_policy.source_created_at).getTime()) / 3600000)) : "неизвестен";
  const policyStamp = (timestamp?: string) => timestamp ? formatInZone(timestamp, zone) : "неизвестно";
  async function action(kind: "approve" | "retry" | "cancel" | "publish-now") {
    setBusy(true);
    setError("");
    try {
      onChanged(await actPublication(event.id, kind, event.version, freshnessOverride));
    } catch (e) {
      setError(
        e instanceof ApiError && e.status === 409
          ? `${e.message} Если выпуск изменился, закройте окно и обновите календарь.`
          : e instanceof Error
            ? e.message
            : String(e),
      );
    } finally {
      setBusy(false);
    }
  }
  async function reschedule() {
    setBusy(true);
    setError("");
    try {
      const [d, t] = value.split("T");
      onChanged(
        await updatePublication(event.id, {
          version: event.version,
          publish_at: zonePartsToISO(d, t, zone),
          timezone: zone,
          freshness_override: freshnessOverride,
        }),
      );
    } catch (e) {
      setError(
        e instanceof ApiError && e.status === 409
          ? `${e.message} Если выпуск изменился или загрузка началась, обновите календарь.`
          : e instanceof Error
            ? e.message
            : String(e),
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <div
      className="fixed inset-0 z-50 bg-black/70 flex items-center justify-center p-4"
      onClick={() => !busy && onClose()}
    >
      <section
        role="dialog"
        aria-modal="true"
        aria-labelledby="event-title"
        className="bg-slate-800 border border-slate-600 rounded-xl p-5 w-full max-w-xl max-h-[90vh] overflow-y-auto flex flex-col gap-4"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex justify-between">
          <h2 id="event-title" className="font-semibold">
            {event.title || event.clip_id}
          </h2>
          <button
            aria-label="Close publication"
            disabled={busy}
            onClick={onClose}
          >
            ×
          </button>
        </div>
        <p className="text-sm text-slate-400">
          {event.account_name} · {event.platform} · {event.state} · render
          revision {event.revision_id ?? "original"}
        </p>
        {twitchPublication && event.state === "on_hold" && event.error === "Выберите время публикации в календаре" && <p className="rounded-lg border border-indigo-900 p-3 text-sm text-indigo-200">Версия готова и допущена автоматически. Выберите время ниже и нажмите «Назначить время». Автопубликация потока выключена, поэтому выпуск ожидает вашего расписания.</p>}
        <Link
          className="text-indigo-400 text-sm"
          href={`/admin/clips/${encodeURIComponent(event.clip_id)}`}
        >
          Open clip editor →
        </Link>
        <label className="flex flex-col gap-1 text-xs text-slate-400">
          Upload starts ({zone})
          <input
            type="datetime-local"
            className={cls}
            value={value}
            disabled={busy || isStarted(event)}
            onChange={(e) => setValue(e.target.value)}
          />
        </label>
        {twitchPublication && !isStarted(event) && <div className="rounded-lg border border-amber-900 bg-amber-950/20 p-3 text-xs">
          {event.twitch_policy && <><p className="text-slate-300">Самый старый исходник создан {policyStamp(event.twitch_policy.source_created_at)}. Возраст к выбранной дате: {selectedAge} ч при максимуме {event.twitch_policy.max_source_age_hours} ч.</p><p className="mt-1 text-slate-400">Крайний срок: {policyStamp(event.twitch_policy.deadline)}. Горизонт {event.twitch_policy.horizon_hours} ч: до {policyStamp(event.twitch_policy.horizon_end)}.</p>{(new Date(selectedAt) > new Date(event.twitch_policy.deadline || "") || new Date(selectedAt) > new Date(event.twitch_policy.horizon_end)) && <p className="mt-2 text-amber-200">Выбранная дата нарушает ограничения. Выберите более раннее время или явно подтвердите исключение.</p>}</>}
          {policyViolation && <><p className="text-amber-200">{policyViolation.message}</p><p className="mt-1 text-slate-400">Самый старый исходник: {policyStamp(policyViolation.source_created_at)}. Крайний срок: {policyStamp(policyViolation.deadline)}.</p></>}
          {event.freshness_override && <p className="mt-2 text-emerald-300">Для сохранённых версии и времени уже подтверждено исключение. Новая дата требует повторного подтверждения.</p>}
          <p className="mt-2 text-slate-400">Назначение Twitch-выпуска проверяет горизонт календаря и возраст всех исходников. Если выбранная дата выходит за ограничения, сервер укажет причину.</p>
          <label className="mt-3 flex items-start gap-2 text-amber-100"><input type="checkbox" className="mt-0.5" checked={freshnessOverride} disabled={busy} onChange={(event) => setFreshnessOverride(event.target.checked)} /><span>Я подтверждаю исключение из правил актуальности и горизонта для этой версии и даты. Решение будет сохранено; изменение даты или версии потребует нового подтверждения.</span></label>
          <p className="mt-2 text-slate-500">Кнопка переноса применяет выбранную дату. «Upload now» применяется к сохранённому плану, а не к несохранённому полю даты.</p>
        </div>}
        <button
          className={button}
          disabled={
            busy ||
            isStarted(event) ||
            ["cancelled", "canceled"].includes(event.state)
          }
          onClick={reschedule}
        >
          {twitchPublication ? "Назначить время" : "Reschedule this account"}
        </button>
        <div className="grid grid-cols-2 gap-2">
          {[
            "held",
            "on_hold",
            "legacy_hold",
            "pending_approval",
            "needs_attention",
          ].includes(event.state) &&
            !isStarted(event) && !twitchPublication && (
              <button
                disabled={busy}
                className="bg-indigo-600 disabled:opacity-40 rounded-lg p-2 text-sm"
                onClick={() => setConfirmation("approve")}
              >
                Confirm and approve
              </button>
            )}
          {(["failed", "retry_wait"].includes(event.state) ||
            (event.state === "needs_attention" && isStarted(event))) && (
            <button
              disabled={busy}
              className={button}
              onClick={() => setConfirmation("retry")}
            >
              Retry
            </button>
          )}
          {event.state === "blocked" && (
            <Link
              href="/admin/accounts"
              className="text-indigo-400 p-2 text-sm"
            >
              Check account connection →
            </Link>
          )}
          {!isStarted(event) &&
            ![
              "cancelled",
              "canceled",
              "held",
              "on_hold",
              "legacy_hold",
              "blocked",
              "needs_attention",
            ].includes(event.state) && (
              <button
                disabled={busy}
                className={button}
                onClick={() => setConfirmation("publish-now")}
              >
                Upload now
              </button>
            )}
          {event.state !== "published" &&
            !["cancelled", "canceled"].includes(event.state) && (
              <button
                disabled={busy}
                className="bg-slate-700 text-red-300 disabled:opacity-40 rounded-lg p-2 text-sm"
                onClick={() => setConfirmation("cancel")}
              >
                Cancel
              </button>
            )}
        </div>
        {event.error && <p className={event.error === "Выберите время публикации в календаре" ? "text-indigo-200 text-sm" : "text-red-400 text-sm"}>{event.error}</p>}
        {event.external_url && (
          <a
            className="text-indigo-400 text-sm"
            target="_blank"
            rel="noopener noreferrer"
            href={event.external_url}
          >
            Open on YouTube →
          </a>
        )}
        {event.started_at && (
          <p className="text-xs text-slate-400">
            Upload started: {formatInZone(event.started_at, zone)}
          </p>
        )}
        {event.uploaded_at && (
          <p className="text-xs text-slate-400">
            Uploaded: {formatInZone(event.uploaded_at, zone)}
          </p>
        )}
        {event.processed_at && (
          <p className="text-xs text-slate-400">
            Processed: {formatInZone(event.processed_at, zone)}
          </p>
        )}
        {event.cancel_requested && (
          <p className="text-amber-400 text-sm">
            Cancellation requested; reconciling provider state.
          </p>
        )}
        {event.published_at && (
          <p className="text-sm text-emerald-400">
            Public: {formatInZone(event.published_at, zone)}
          </p>
        )}
        {error && (
          <p role="alert" className="text-red-400 text-sm">
            {error}
          </p>
        )}
        <h3 className="text-xs font-semibold uppercase text-slate-400">
          History
        </h3>
        {history.length ? (
          history.map((h, i) => (
            <div
              key={h.id ?? i}
              className="text-xs text-slate-400 border-t border-slate-700 pt-2"
            >
              <p>
                {formatInZone(h.created_at, zone)} · {h.kind} · {h.state}
              </p>
              {!!h.detail && (
                <p className="break-all mt-1">
                  {typeof h.detail === "string"
                    ? h.detail
                    : JSON.stringify(h.detail)}
                </p>
              )}
            </div>
          ))
        ) : (
          <p className="text-xs text-slate-500">No recorded events.</p>
        )}
      </section>
      {confirmation && (
        <ConfirmAction
          title="Confirm publication action"
          message={
            confirmation === "publish-now"
              ? "Start uploading this publication now?"
              : confirmation === "cancel"
                ? "Cancel this publication? An external request already in progress may finish before cancellation is recorded."
                : confirmation === "approve"
                  ? "Confirm this older or held publication and allow automatic catch-up?"
                  : "Retry this failed publication using its saved upload checkpoint?"
          }
          onCancel={() => setConfirmation(null)}
          onConfirm={() => {
            const pending = confirmation;
            setConfirmation(null);
            void action(pending);
          }}
        />
      )}
    </div>
  );
}
