"use client";
import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import NewsNav from "@/components/news/NewsNav";
import PostPreview from "@/components/news/PostPreview";
import ConfirmAction from "@/components/ui/ConfirmAction";
import {
  approveNewsPost,
  fetchNewsCalendar,
  fetchNewsChannels,
  fetchNewsFlows,
  fetchNewsPosts,
  holdNewsPost,
  rejectNewsPost,
} from "@/lib/news";
import {
  CALENDAR_TONE,
  POST_STATUS_LABEL,
  postWhen,
  telegramPostURL,
} from "@/lib/news-flow";
import {
  addDays,
  calendarDays,
  DEFAULT_TIMEZONE,
  formatInZone,
  shiftMonth,
  TIMEZONES,
  zonedParts,
  zonePartsToISO,
} from "@/lib/datetime";
import type {
  NewsChannel,
  NewsFlow,
  NewsPost,
  NewsPostStatus,
} from "@/types/news";

const cls =
  "bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 [color-scheme:dark]";
const button =
  "bg-slate-700 hover:bg-slate-600 disabled:opacity-40 rounded-lg px-3 py-2 text-sm";
const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));
const STATUSES: NewsPostStatus[] = [
  "approved",
  "published",
  "failed",
  "pending",
  "rejected",
  "expired",
];

export default function NewsCalendarPage() {
  const [view, setView] = useState<"week" | "month">("week");
  const [zone, setZone] = useState(DEFAULT_TIMEZONE);
  const [date, setDate] = useState("");
  const [flowID, setFlowID] = useState(0);
  const [state, setState] = useState<NewsPostStatus | "active" | "">("active");
  const [flows, setFlows] = useState<NewsFlow[]>([]);
  const [channels, setChannels] = useState<NewsChannel[]>([]);
  const [posts, setPosts] = useState<NewsPost[]>([]);
  const [drafts, setDrafts] = useState<NewsPost[]>([]);
  const [selected, setSelected] = useState<NewsPost | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [observedAt, setObservedAt] = useState("");
  const [updatedAt, setUpdatedAt] = useState("");

  const days = useMemo(
    () => (date ? calendarDays(date, view) : []),
    [date, view],
  );
  const today = observedAt ? zonedParts(observedAt, zone).date : "";

  useEffect(() => {
    const now = new Date().toISOString();
    setObservedAt(now);
    setDate(zonedParts(now).date);
  }, []);
  useEffect(() => {
    void Promise.allSettled([fetchNewsFlows(), fetchNewsChannels()]).then(
      ([f, c]) => {
        if (f.status === "fulfilled") setFlows(f.value ?? []);
        if (c.status === "fulfilled") setChannels(c.value ?? []);
      },
    );
  }, []);

  useEffect(() => {
    if (!days.length) return;
    let active = true;
    const controller = new AbortController();
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
    Promise.all([
      fetchNewsCalendar(from, to, flowID, controller.signal),
      fetchNewsPosts("pending", flowID, controller.signal),
    ])
      .then(([rows, pending]) => {
        if (!active) return;
        setPosts(rows ?? []);
        setDrafts((pending ?? []).filter((p) => !p.scheduled_at));
        const now = new Date().toISOString();
        setUpdatedAt(now);
        setObservedAt(now);
      })
      .catch((e) => {
        if (active) setError(msg(e));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
      controller.abort();
    };
  }, [days, zone, flowID, refresh]);

  const visible = useMemo(
    () =>
      posts.filter((p) =>
        state === ""
          ? true
          : state === "active"
            ? p.status !== "rejected" && p.status !== "expired"
            : p.status === state,
      ),
    [posts, state],
  );
  const groups = useMemo(() => {
    const map = new Map<string, NewsPost[]>();
    for (const p of visible) {
      const when = postWhen(p);
      if (!when) continue;
      const key = zonedParts(when, zone).date;
      map.set(key, [...(map.get(key) ?? []), p]);
    }
    return map;
  }, [visible, zone]);

  function changed(row: NewsPost) {
    setSelected(row);
    setRefresh((v) => v + 1);
  }

  return (
    <div className="flex flex-col gap-5 p-4 sm:p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">Календарь публикаций</h1>
        <Link href="/admin/news/posts" className="text-sm text-indigo-400">
          Посты на ревью →
        </Link>
      </div>
      <NewsNav />
      <p className="text-sm text-slate-400">
        Время — когда пост уходит в Telegram. Опубликованные посты стоят на
        времени фактической отправки. Черновики без времени показаны отдельно:
        откройте черновик и назначьте время, чтобы он встал в календарь.
      </p>
      <p className="text-xs text-slate-400">
        Автообновления нет: нажмите «Обновить», чтобы загрузить свежие данные.
        {updatedAt && ` Загружено: ${formatInZone(updatedAt, zone)}.`}
      </p>

      <div className="flex flex-wrap items-center gap-2">
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
          Сегодня
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
          aria-label="Дата"
          type="date"
          className={cls}
          value={date}
          onChange={(e) => e.target.value && setDate(e.target.value)}
        />
        <select
          aria-label="Вид"
          className={cls}
          value={view}
          onChange={(e) => setView(e.target.value as "week" | "month")}
        >
          <option value="week">Неделя</option>
          <option value="month">Месяц</option>
        </select>
        <select
          aria-label="Часовой пояс"
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
          {loading ? "Обновляем…" : "Обновить"}
        </button>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <select
          aria-label="Поток"
          className={cls}
          value={flowID}
          onChange={(e) => setFlowID(Number(e.target.value))}
        >
          <option value={0}>Все потоки</option>
          {flows.map((f) => (
            <option key={f.id} value={f.id}>
              {f.name}
            </option>
          ))}
        </select>
        <select
          aria-label="Статус"
          className={cls}
          value={state}
          onChange={(e) =>
            setState(e.target.value as NewsPostStatus | "active" | "")
          }
        >
          <option value="active">Активные</option>
          <option value="">Все (с отклонёнными)</option>
          {STATUSES.map((s) => (
            <option key={s} value={s}>
              {POST_STATUS_LABEL[s]}
            </option>
          ))}
        </select>
        <span className="self-center text-xs text-slate-500">
          {visible.length} постов
        </span>
      </div>

      {error && (
        <p role="alert" className="text-red-400">
          {error}
          {updatedAt &&
            " Ранее загруженные посты сохранены. Нажмите «Обновить», чтобы повторить."}
        </p>
      )}

      {drafts.length > 0 && (
        <section className="rounded-xl border border-amber-900 bg-amber-950/20 p-3">
          <p className="mb-2 text-sm text-amber-200">
            Черновики без времени ({drafts.length}): откройте и назначьте время
            публикации.
          </p>
          <div className="flex flex-wrap gap-2">
            {drafts.map((p) => (
              <button
                key={p.id}
                onClick={() => setSelected(p)}
                className={`max-w-xs truncate rounded-md border px-2 py-1 text-left text-xs ${CALENDAR_TONE.pending}`}
                title={p.title}
              >
                {p.flow_name} · {p.emoji} {p.title || p.text.slice(0, 40)}
              </button>
            ))}
          </div>
        </section>
      )}

      <div className="overflow-x-auto">
        <div className="grid min-w-[800px] grid-cols-7 gap-2">
          {["Пн", "Вт", "Ср", "Чт", "Пт", "Сб", "Вс"].map((d) => (
            <p key={d} className="px-2 text-xs text-slate-500">
              {d}
            </p>
          ))}
          {days.map((day) => (
            <div
              key={day}
              className={`flex min-h-40 flex-col gap-2 rounded-xl border p-2 ${day === today ? "border-indigo-500 bg-slate-800" : "border-slate-700 bg-slate-800/50"}`}
            >
              <p className="text-xs text-slate-400">
                {day.slice(8)}.{day.slice(5, 7)}
              </p>
              {(groups.get(day) ?? []).map((p) => (
                <button
                  key={p.id}
                  onClick={() => setSelected(p)}
                  className={`flex flex-col gap-1 rounded-md border p-2 text-left ${CALENDAR_TONE[p.status]}`}
                >
                  <span className="text-xs font-medium">
                    {zonedParts(postWhen(p)!, zone).time} · {p.flow_name}
                  </span>
                  <span className="truncate text-xs">
                    {p.emoji} {p.title || p.text.slice(0, 50)}
                  </span>
                  <span className="text-[10px] text-slate-400">
                    {POST_STATUS_LABEL[p.status]}
                    {p.image_url ? " · 🖼" : ""}
                  </span>
                </button>
              ))}
            </div>
          ))}
        </div>
      </div>

      {selected && (
        <PostModal
          key={selected.id}
          post={selected}
          zone={zone}
          flow={flows.find((f) => f.id === selected.flow_id)}
          channels={channels}
          onClose={() => setSelected(null)}
          onChanged={changed}
        />
      )}
    </div>
  );
}

function PostModal({
  post,
  zone,
  flow,
  channels,
  onClose,
  onChanged,
}: {
  post: NewsPost;
  zone: string;
  flow?: NewsFlow;
  channels: NewsChannel[];
  onClose: () => void;
  onChanged: (p: NewsPost) => void;
}) {
  const initial = postWhen(post);
  const [value, setValue] = useState(() => {
    const p = zonedParts(initial ?? new Date().toISOString(), zone);
    return `${p.date}T${p.time}`;
  });
  const [confirm, setConfirm] = useState<"now" | "reject" | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    function key(e: KeyboardEvent) {
      if (e.key === "Escape" && !busy && !confirm) onClose();
    }
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [onClose, busy, confirm]);

  const channel = channels.find((c) => c.id === flow?.channel_id);
  const link =
    post.status === "published" && channel
      ? telegramPostURL(channel.chat_id, post.message_id)
      : null;
  const editable =
    post.status === "pending" ||
    post.status === "approved" ||
    post.status === "failed";

  async function run(work: () => Promise<NewsPost>) {
    setBusy(true);
    setError("");
    try {
      onChanged(await work());
    } catch (e) {
      setError(msg(e));
    } finally {
      setBusy(false);
      setConfirm(null);
    }
  }
  function chosenISO(): string | null {
    try {
      const [d, t] = value.split("T");
      return zonePartsToISO(d, t, zone);
    } catch {
      setError("Укажите дату и время.");
      return null;
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4"
      onClick={() => !busy && onClose()}
    >
      <section
        role="dialog"
        aria-modal="true"
        aria-labelledby="post-title"
        className="flex max-h-[90vh] w-full max-w-xl flex-col gap-4 overflow-y-auto rounded-xl border border-slate-600 bg-slate-800 p-5"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex justify-between gap-3">
          <h2 id="post-title" className="font-semibold">
            {post.title || "Пост"}
          </h2>
          <button aria-label="Закрыть" disabled={busy} onClick={onClose}>
            ×
          </button>
        </div>
        <p className="text-sm text-slate-400">
          {post.flow_name} · {POST_STATUS_LABEL[post.status]} · создан{" "}
          {formatInZone(post.created_at, zone)}
          {channel ? ` · ${channel.title}` : ""}
        </p>

        <PostPreview
          title={post.title}
          emoji={post.emoji}
          text={post.text}
          hashtags={post.hashtags}
          links={post.links}
          imageURL={post.image_url}
          credit={post.image_source === "pexels" ? post.image_credit : ""}
          creditURL={post.image_credit_url}
          footer={flow?.footer ?? ""}
        />

        {post.status === "published" && (
          <p className="text-sm text-emerald-400">
            Опубликован:{" "}
            {post.published_at ? formatInZone(post.published_at, zone) : "—"}
            {link && (
              <>
                {" "}
                ·{" "}
                <a
                  className="text-indigo-300 underline"
                  href={link}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  Открыть в Telegram →
                </a>
              </>
            )}
          </p>
        )}
        {post.error && <p className="text-sm text-red-400">{post.error}</p>}
        {post.status === "failed" && (
          <p className="text-xs text-slate-400">
            Попыток отправки: {post.attempts}. «Назначить время» или
            «Опубликовать сейчас» повторят отправку.
          </p>
        )}

        {editable && (
          <label className="flex flex-col gap-1 text-xs text-slate-400">
            Время публикации ({zone})
            <input
              type="datetime-local"
              className={cls}
              value={value}
              disabled={busy}
              onChange={(e) => setValue(e.target.value)}
            />
          </label>
        )}

        <div className="grid grid-cols-2 gap-2">
          {editable && (
            <button
              className="rounded-lg bg-indigo-600 p-2 text-sm text-white disabled:opacity-40"
              disabled={busy || !value}
              onClick={() => {
                const iso = chosenISO();
                if (iso) void run(() => approveNewsPost(post.id, iso));
              }}
            >
              {post.status === "approved" ? "Перенести" : "Назначить время"}
            </button>
          )}
          {editable && (
            <button
              className={button}
              disabled={busy}
              onClick={() => setConfirm("now")}
            >
              Опубликовать сейчас
            </button>
          )}
          {post.status === "approved" && (
            <button
              className={button}
              disabled={busy}
              onClick={() => void run(() => holdNewsPost(post.id))}
            >
              Снять с очереди
            </button>
          )}
          {editable && (
            <button
              className="rounded-lg bg-slate-700 p-2 text-sm text-red-300 disabled:opacity-40"
              disabled={busy}
              onClick={() => setConfirm("reject")}
            >
              Отклонить
            </button>
          )}
        </div>
        {editable && (
          <Link
            href={`/admin/news/posts?flow=${post.flow_id}&status=${post.status}`}
            className="text-sm text-indigo-400"
          >
            Править текст и картинку на странице «Посты» →
          </Link>
        )}
        {error && (
          <p role="alert" className="text-sm text-red-400">
            {error}
          </p>
        )}
      </section>
      {confirm === "now" && (
        <ConfirmAction
          title="Опубликовать сейчас?"
          message="Пост сразу уйдёт в Telegram-канал независимо от выбранного времени."
          confirmLabel="Опубликовать"
          onCancel={() => setConfirm(null)}
          onConfirm={() => void run(() => approveNewsPost(post.id))}
        />
      )}
      {confirm === "reject" && (
        <ConfirmAction
          title="Отклонить пост?"
          message="Пост не будет опубликован и исчезнет из очереди."
          confirmLabel="Отклонить"
          onCancel={() => setConfirm(null)}
          onConfirm={() => void run(() => rejectNewsPost(post.id))}
        />
      )}
    </div>
  );
}
