"use client";
import { useCallback, useEffect, useState } from "react";
import NewsNav from "@/components/news/NewsNav";
import ConfirmAction from "@/components/ui/ConfirmAction";
import {
  createNewsChannel,
  deleteNewsChannel,
  fetchNewsChannels,
  fetchNewsStatus,
  testNewsChannel,
  updateNewsChannel,
  verifyNewsChannel,
} from "@/lib/news";
import { relativeTime } from "@/lib/news-flow";
import type { NewsChannel, NewsStatus } from "@/types/news";

const input = "w-full rounded-lg border border-slate-600 bg-slate-700 px-3 py-2 text-sm";
const button = "rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600 disabled:opacity-40";
const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));

const BADGE: Record<NewsChannel["status"], [string, string]> = {
  ok: ["Подключён", "bg-emerald-500/15 text-emerald-300"],
  error: ["Ошибка", "bg-red-500/15 text-red-300"],
  unverified: ["Не проверен", "bg-amber-500/15 text-amber-300"],
};

export default function NewsChannelsPage() {
  const [channels, setChannels] = useState<NewsChannel[]>([]);
  const [status, setStatus] = useState<NewsStatus | null>(null);
  const [form, setForm] = useState({ title: "", chat_id: "", bot_token: "" });
  const [newToken, setNewToken] = useState<Record<number, string>>({});
  const [remove, setRemove] = useState<NewsChannel | null>(null);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  const load = useCallback(async () => {
    const [c, s] = await Promise.allSettled([fetchNewsChannels(), fetchNewsStatus()]);
    if (c.status === "fulfilled") setChannels(c.value ?? []);
    if (s.status === "fulfilled") setStatus(s.value);
    setError(c.status === "rejected" ? msg(c.reason) : "");
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  async function run(key: string, work: () => Promise<string | void>) {
    setBusy(key);
    setError("");
    setNotice("");
    try {
      const done = await work();
      if (done) setNotice(done);
      await load();
    } catch (e) {
      setError(msg(e));
    } finally {
      setBusy("");
    }
  }

  const keyMissing = status !== null && !status.secret_key_ready;

  return (
    <div className="flex flex-col gap-5 p-4 sm:p-6">
      <h1 className="text-xl font-semibold">Telegram-каналы</h1>
      <NewsNav />
      {error && <p role="alert" className="text-sm text-red-400">{error}</p>}
      {notice && <p role="status" className="text-sm text-emerald-400">{notice}</p>}
      {keyMissing && (
        <p role="alert" className="rounded-lg border border-amber-600/50 bg-amber-500/10 p-3 text-sm text-amber-300">
          На сервере не задан NEWS_SECRET_KEY, поэтому токены ботов сохранить нельзя. Добавьте ключ в Infisical (32 случайных байта в base64) и перезапустите news-collector.
        </p>
      )}

      <section className="rounded-xl border border-slate-700 bg-slate-800 p-4">
        <h2 className="mb-1 text-sm font-semibold">Подключить канал</h2>
        <ol className="mb-3 list-decimal space-y-1 pl-5 text-xs text-slate-400">
          <li>Создайте бота у @BotFather и скопируйте токен.</li>
          <li>Добавьте бота администратором в канал с правом публиковать сообщения.</li>
          <li>Укажите канал (@username, ссылку t.me или числовой id) — доступ проверится автоматически.</li>
        </ol>
        <div className="grid gap-3 sm:grid-cols-3">
          <label className="text-xs text-slate-400">Название
            <input className={input} value={form.title} onChange={(e) => setForm({ ...form, title: e.target.value })} placeholder="Крипто-новости" maxLength={120} />
          </label>
          <label className="text-xs text-slate-400">Канал
            <input className={input} value={form.chat_id} onChange={(e) => setForm({ ...form, chat_id: e.target.value })} placeholder="@my_news или https://t.me/my_news" />
          </label>
          <label className="text-xs text-slate-400">Токен бота
            <input className={input} type="password" autoComplete="off" value={form.bot_token} onChange={(e) => setForm({ ...form, bot_token: e.target.value })} placeholder="123456789:AA…" />
          </label>
        </div>
        <button
          type="button"
          className="mt-3 rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white hover:bg-indigo-500 disabled:opacity-40"
          disabled={busy !== "" || keyMissing || !form.title.trim() || !form.chat_id.trim() || !form.bot_token.trim()}
          onClick={() =>
            void run("create", async () => {
              const created = await createNewsChannel(form);
              setForm({ title: "", chat_id: "", bot_token: "" });
              return created.status === "ok" ? `Канал «${created.chat_title || created.title}» подключён.` : `Канал сохранён, но проверка не пройдена: ${created.last_error}`;
            })
          }
        >
          {busy === "create" ? "Подключаем…" : "Подключить"}
        </button>
      </section>

      <section className="flex flex-col gap-3">
        {channels.length === 0 && <p className="text-sm text-slate-400">Каналов пока нет.</p>}
        {channels.map((c) => {
          const [label, style] = BADGE[c.status];
          return (
            <article key={c.id} className="rounded-xl border border-slate-700 bg-slate-800 p-4">
              <div className="flex flex-wrap items-center gap-3">
                <h3 className="font-medium">{c.title}</h3>
                <span className={`rounded-full px-2 py-0.5 text-xs ${style}`}>{label}</span>
                <span className="text-xs text-slate-400">
                  {c.chat_id}
                  {c.chat_title ? ` · ${c.chat_title}` : ""}
                  {c.bot_username ? ` · бот @${c.bot_username}` : ""}
                  {c.verified_at ? ` · проверен ${relativeTime(c.verified_at)}` : ""}
                </span>
              </div>
              {c.last_error && <p className="mt-2 text-sm text-red-400">{c.last_error}</p>}
              <div className="mt-3 flex flex-wrap items-center gap-2">
                <button type="button" className={button} disabled={busy !== ""} onClick={() => void run(`v${c.id}`, async () => { const v = await verifyNewsChannel(c.id); return v.status === "ok" ? "Доступ подтверждён." : undefined; })}>
                  Проверить доступ
                </button>
                <button type="button" className={button} disabled={busy !== "" || c.status !== "ok"} onClick={() => void run(`t${c.id}`, async () => { await testNewsChannel(c.id); return "Тестовое сообщение отправлено в канал."; })}>
                  Тестовое сообщение
                </button>
                <input
                  className="w-56 rounded-lg border border-slate-600 bg-slate-700 px-3 py-2 text-sm"
                  type="password"
                  autoComplete="off"
                  placeholder="Новый токен бота"
                  value={newToken[c.id] ?? ""}
                  onChange={(e) => setNewToken({ ...newToken, [c.id]: e.target.value })}
                />
                <button type="button" className={button} disabled={busy !== "" || !(newToken[c.id] ?? "").trim()} onClick={() => void run(`r${c.id}`, async () => { await updateNewsChannel(c.id, { bot_token: newToken[c.id] }); setNewToken({ ...newToken, [c.id]: "" }); return "Токен заменён и проверен."; })}>
                  Заменить токен
                </button>
                <button type="button" className="ml-auto rounded-lg bg-red-900/60 px-3 py-2 text-sm text-red-200 hover:bg-red-800 disabled:opacity-40" disabled={busy !== ""} onClick={() => setRemove(c)}>
                  Удалить
                </button>
              </div>
            </article>
          );
        })}
      </section>
      {remove && (
        <ConfirmAction
          title="Удалить канал?"
          message={`«${remove.title}» будет удалён вместе с токеном. Потоки, которые публиковали в него, останутся без канала и перестанут генерировать посты.`}
          confirmLabel="Удалить"
          onCancel={() => setRemove(null)}
          onConfirm={() => {
            const target = remove;
            setRemove(null);
            void run(`d${target.id}`, async () => { await deleteNewsChannel(target.id); return "Канал удалён."; });
          }}
        />
      )}
    </div>
  );
}
