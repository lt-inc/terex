"use client";

import { useCallback, useEffect, useId, useRef, useState } from "react";
import Link from "next/link";
import {
  fetchAccountsList,
  fetchPublicationHistory,
  fetchPublicationOptions,
  publishClipDirect,
  reconcileTikTokPublication,
  actPublication,
} from "@/lib/api";
import {
  MEDIA_EXPLANATION,
  MEDIA_LABEL,
  accountReadiness,
  clearPendingRequest,
  isDefiniteRejection,
  isTerminalPublication,
  loadPendingRequest,
  newRequestID,
  publicationStateLabel,
  readableReason,
  savePendingRequest,
  visibilityOptions,
} from "@/lib/direct-publish";
import { formatInZone } from "@/lib/datetime";
import type {
  AccountRecord,
  DirectMediaKind,
  DirectPublicationOptions,
  DirectPublishRequest,
  DirectVisibility,
  PublicationEvent,
  PublicationHistory,
} from "@/types";
import PreviewModal, {
  type PreviewTarget,
} from "@/components/media/PreviewModal";

const field =
  "bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 w-full min-w-0";
const button =
  "bg-slate-700 hover:bg-slate-600 disabled:opacity-40 px-3 py-2 rounded-lg text-sm";
const PLATFORM_LABEL: Record<string, string> = {
  youtube: "YouTube",
  tiktok: "TikTok",
};
const SEND_TIMEOUT_MS = 60_000;

// A request already on the wire is never sent twice, even from a reopened dialog.
const inFlight = new Set<string>();

const storage = () => {
  try { return typeof window === "undefined" ? null : window.localStorage; }
  catch { return null; }
};
const message = (e: unknown) => (e instanceof Error ? e.message : String(e))
  .replace(/^state or version changed; reload and retry:\s*/, "")
  .replace(/^state or version changed; reload and retry$/, "Версия или состояние изменились. Обновите данные и проверьте публикацию.")
  .replace(/^invalid input:\s*/, "");

export default function DirectPublishDialog({
  clipID,
  clipTitle,
  initialSection = "publish",
  onClose,
}: {
  clipID: string;
  clipTitle: string;
  initialSection?: "publish" | "history";
  onClose: () => void;
}) {
  const titleID = useId();
  const dialog = useRef<HTMLElement>(null);
  const historySection = useRef<HTMLElement>(null);
  const twitchClip =
    clipID.startsWith("twitch-") || clipID.startsWith("compilation-");
  const [options, setOptions] = useState<DirectPublicationOptions | null>(null);
  const [optionsError, setOptionsError] = useState("");
  const [accounts, setAccounts] = useState<AccountRecord[] | null>(null);
  const [accountsError, setAccountsError] = useState("");
  const [locked, setLocked] = useState<DirectPublishRequest | null>(() =>
    loadPendingRequest(storage(), clipID),
  );
  const [accountID, setAccountID] = useState("");
  const [mediaKind, setMediaKind] = useState<DirectMediaKind | "">("");
  const [visibility, setVisibility] = useState<DirectVisibility>("private");
  const [caption, setCaption] = useState("");
  const captionTouched = useRef(false);
  const [freshness, setFreshness] = useState(false);
  const [previewed, setPreviewed] = useState<string[]>([]);
  const [confirmed, setConfirmed] = useState("");
  const [preview, setPreview] = useState<PreviewTarget | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const sending = useRef(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const optionsRef = useRef<DirectPublicationOptions | null>(null);
  const pollNow = useRef<() => void>(() => {});

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    dialog.current
      ?.querySelector<HTMLElement>("select, textarea, button")
      ?.focus();
    return () => {
      document.body.style.overflow = overflow;
      previous?.focus();
    };
  }, []);

  useEffect(() => {
    if (initialSection === "history")
      historySection.current?.scrollIntoView({ block: "start" });
  }, [initialSection]);

  useEffect(() => {
    const controller = new AbortController();
    fetchAccountsList(controller.signal)
      .then(setAccounts)
      .catch((e) => {
        if (!controller.signal.aborted) setAccountsError(message(e));
      });
    return () => controller.abort();
  }, []);

  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let controller: AbortController | undefined;
    let seq = 0;
    async function tick() {
      clearTimeout(timer);
      controller?.abort();
      controller = new AbortController();
      const mine = ++seq;
      try {
        const next = await fetchPublicationOptions(clipID, controller.signal);
        if (stopped || mine !== seq) return;
        optionsRef.current = next;
        setOptions(next);
        setOptionsError("");
      } catch (e) {
        if (stopped || mine !== seq) return;
        setOptionsError(message(e));
      }
      if (stopped || mine !== seq) return;
      const active = (optionsRef.current?.publications ?? []).some(
        (p) => !isTerminalPublication(p.state),
      );
      timer = setTimeout(tick, active ? 3000 : 15000);
    }
    pollNow.current = () => void tick();
    void tick();
    return () => {
      stopped = true;
      clearTimeout(timer);
      controller?.abort();
    };
  }, [clipID]);

  useEffect(() => {
    if (options && !captionTouched.current) setCaption(options.caption ?? "");
  }, [options]);

  const variants = options?.variants ?? [];
  useEffect(() => {
    if (!options) return;
    const current = options.variants.find((v) => v.media_kind === mediaKind);
    if (current?.eligible) return;
    const first =
      options.variants.find((v) => v.eligible && v.media_kind === "rendered") ??
      options.variants.find((v) => v.eligible);
    setMediaKind(first?.media_kind ?? "");
  }, [options, mediaKind]);

  const account = accounts?.find((a) => String(a.id) === accountID);
  const platform =
    account?.platform === "youtube" || account?.platform === "tiktok"
      ? account.platform
      : null;
  const visibilityChoices = visibilityOptions(platform ?? "youtube");
  const visibilityAllowed = visibilityChoices.some(
    (o) => o.value === visibility,
  );
  useEffect(() => {
    if (!visibilityAllowed) setVisibility("private");
  }, [visibilityAllowed]);

  const reviewKey =
    options && mediaKind ? `${options.revision_id}:${mediaKind}` : "";
  const reviewed = !!reviewKey && confirmed === reviewKey;
  const variant = variants.find((v) => v.media_kind === mediaKind);
  const canPublish =
    !locked &&
    !submitting &&
    !!options &&
    !!platform &&
    !!account &&
    accountReadiness(account).ready &&
    !!variant?.eligible &&
    (!variant.platforms || variant.platforms.includes(platform)) &&
    !options.publications.some((p) => p.account_id === account.id) &&
    caption.trim().length > 0 &&
    visibilityAllowed &&
    reviewed;

  const send = useCallback(
    async (request: DirectPublishRequest) => {
      if (sending.current || inFlight.has(request.request_id)) return;
      sending.current = true;
      inFlight.add(request.request_id);
      setSubmitting(true);
      setError("");
      setNotice("");
      savePendingRequest(storage(), clipID, request);
      setLocked(request);
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), SEND_TIMEOUT_MS);
      try {
        const event = await publishClipDirect(
          clipID,
          request,
          controller.signal,
        );
        if (!event || typeof event.id !== "number")
          throw new Error("Ответ сервера не удалось прочитать");
        clearPendingRequest(storage(), clipID);
        setLocked(null);
        setConfirmed("");
        setFreshness(false);
        setNotice(
          `Публикация принята: ${event.account_name} · ${publicationStateLabel(event.state ?? event.status ?? "")}. Ниже можно следить за статусом.`,
        );
      } catch (e) {
        if (isDefiniteRejection(e)) {
          clearPendingRequest(storage(), clipID);
          setLocked(null);
          setConfirmed("");
          setError(
            `Публикация не принята: ${message(e)}. Ничего не отправлено — можно изменить данные и попробовать снова.`,
          );
        } else {
          setError(
            "Не удалось узнать, принят ли запрос. Он сохранён: нажмите «Повторить запрос» — дубликата публикации не появится.",
          );
        }
      } finally {
        clearTimeout(timer);
        inFlight.delete(request.request_id);
        sending.current = false;
        setSubmitting(false);
        pollNow.current();
      }
    },
    [clipID],
  );

  function publish() {
    if (!canPublish || !options || !platform || !account || !mediaKind) return;
    void send({
      request_id: newRequestID(),
      revision_id: options.revision_id,
      expected_version: options.expected_version,
      media_kind: mediaKind,
      account_id: account.id,
      platform,
      caption,
      visibility,
      reviewed: true,
      ...(freshness ? { freshness_override: true } : {}),
    });
  }

  function openPreview() {
    if (!options || !mediaKind) return;
    setPreviewed((p) => (p.includes(reviewKey) ? p : [...p, reviewKey]));
    setPreview({
      clipID,
      revisionID: options.revision_id,
      kind: mediaKind,
      title: `${clipTitle} — ${MEDIA_LABEL[mediaKind]}`,
    });
  }

  function onKeyDown(e: React.KeyboardEvent) {
    if (e.key === "Escape") {
      e.stopPropagation();
      onClose();
    }
    if (e.key !== "Tab") return;
    const items = dialog.current?.querySelectorAll<HTMLElement>(
      "button:not(:disabled), a[href], select:not(:disabled), textarea:not(:disabled), input:not(:disabled)",
    );
    if (!items?.length) return;
    const first = items[0];
    const last = items[items.length - 1];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  }

  const lockedAccount = locked
    ? accounts?.find((a) => a.id === locked.account_id)
    : undefined;
  const publications = [...(options?.publications ?? [])].sort(
    (a, b) => b.id - a.id,
  );

  return (
    <>
      <div
        className="fixed inset-0 z-40 flex items-center justify-center bg-black/70 p-4"
        onClick={(e) => {
          if (e.target === e.currentTarget) onClose();
        }}
      >
        <section
          ref={dialog}
          role="dialog"
          aria-modal="true"
          aria-labelledby={titleID}
          onKeyDown={onKeyDown}
          className="flex max-h-[92vh] w-full max-w-2xl flex-col gap-5 overflow-y-auto rounded-xl border border-slate-600 bg-slate-800 p-5"
        >
          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
              <h2 id={titleID} className="font-semibold text-slate-100">
                Опубликовать напрямую
              </h2>
              <p className="mt-1 break-words text-sm text-slate-400">
                {clipTitle}
              </p>
            </div>
            <button type="button" className={button} onClick={onClose}>
              Закрыть ✕
            </button>
          </div>

          {optionsError && (
            <p role="alert" className="text-sm text-red-400">
              Не удалось загрузить данные клипа: {optionsError}
            </p>
          )}
          {!options && !optionsError && (
            <p role="status" className="text-sm text-slate-400">
              Загружаем данные клипа…
            </p>
          )}

          {options && (
            <div className="flex flex-col gap-4">
              <p className="text-xs text-slate-400">
                Публикация уходит сразу, без потока и календаря. Расписание не
                используется. Если этот клип уже публиковался на этот аккаунт,
                повторная публикация будет отклонена.
              </p>

              {locked ? (
                <div className="flex flex-col gap-2 rounded-lg border border-amber-700 bg-amber-950/30 p-3 text-sm">
                  <p className="font-medium text-amber-200">
                    Запрос ещё не подтверждён сервером
                  </p>
                  <p className="text-slate-300">
                    {lockedAccount?.name ?? "Аккаунт"} ·{" "}
                    {PLATFORM_LABEL[locked.platform]} ·{" "}
                    {MEDIA_LABEL[locked.media_kind]} ·{" "}
                    {
                      visibilityOptions(locked.platform).find(
                        (o) => o.value === locked.visibility,
                      )?.label
                    }
                  </p>
                  <p className="whitespace-pre-wrap break-words text-xs text-slate-400">
                    {locked.caption || "Без подписи"}
                  </p>
                  <p className="text-xs text-slate-400">
                    Повтор отправит тот же самый запрос: если он уже дошёл,
                    новая публикация не создастся.
                  </p>
                  <div>
                    <button
                      type="button"
                      className={`${button} bg-indigo-600 hover:bg-indigo-500`}
                      disabled={submitting}
                      onClick={() => void send(locked)}
                    >
                      {submitting ? "Отправляем…" : "Повторить запрос"}
                    </button>
                  </div>
                </div>
              ) : (
                <>
                  <fieldset className="flex flex-col gap-2">
                    <legend className="mb-1 text-xs font-semibold uppercase tracking-widest text-slate-400">
                      Что публикуем
                    </legend>
                    {variants.map((v) => (
                      <label
                        key={v.media_kind}
                        className={`flex gap-3 rounded-lg border p-3 text-sm ${v.eligible ? "cursor-pointer" : "opacity-60"} ${mediaKind === v.media_kind ? "border-indigo-500 bg-indigo-950/30" : "border-slate-600"}`}
                      >
                        <input
                          type="radio"
                          name="media_kind"
                          className="mt-1 accent-indigo-500"
                          checked={mediaKind === v.media_kind}
                          disabled={!v.eligible}
                          onChange={() => setMediaKind(v.media_kind)}
                        />
                        <span className="flex flex-col gap-1">
                          <span className="font-medium text-slate-100">
                            {MEDIA_LABEL[v.media_kind]}
                          </span>
                          <span className="text-xs text-slate-400">
                            {MEDIA_EXPLANATION[v.media_kind]}
                          </span>
                          {(!v.eligible || !!v.reason) && (
                            <span className="text-xs text-amber-300">
                              {!v.eligible ? "Недоступно" : "Ограничение"}
                              {readableReason(v.reason)
                                ? `: ${readableReason(v.reason)}`
                                : ""}
                            </span>
                          )}
                        </span>
                      </label>
                    ))}
                    {!variants.some((v) => v.eligible) && (
                      <p className="text-sm text-amber-300">
                        Сейчас нечего публиковать: нет доступного видеофайла.
                      </p>
                    )}
                  </fieldset>

                  <label className="flex flex-col gap-1 text-xs text-slate-400">
                    Аккаунт
                    <select
                      className={field}
                      value={accountID}
                      onChange={(e) => setAccountID(e.target.value)}
                    >
                      <option value="">Выберите аккаунт</option>
                      {(accounts ?? [])
                        .filter(
                          (a) =>
                            a.platform === "youtube" || a.platform === "tiktok",
                        )
                        .map((a) => {
                          const state = accountReadiness(a);
                          return (
                            <option
                              key={a.id}
                              value={a.id}
                              disabled={!state.ready}
                            >
                              {a.name} · {PLATFORM_LABEL[a.platform]}
                              {state.ready ? "" : ` (${state.reason})`}
                            </option>
                          );
                        })}
                    </select>
                    {accountsError && (
                      <span role="alert" className="text-red-400">
                        Не удалось загрузить аккаунты: {accountsError}
                      </span>
                    )}
                    {account && options.publications.some((p) => p.account_id === account.id) && (
                      <span className="text-amber-300">Для этого аккаунта уже есть публикация. Её состояние и безопасные действия доступны ниже.</span>
                    )}
                    {accounts &&
                      !accounts.some((a) => accountReadiness(a).ready) && (
                        <span className="text-amber-300">
                          Нет подключённых YouTube или TikTok аккаунтов.{" "}
                          <Link href="/admin/accounts" className="underline">
                            Подключить →
                          </Link>
                        </span>
                      )}
                  </label>

                  <label className="flex flex-col gap-1 text-xs text-slate-400">
                    Доступ к видео
                    <select
                      className={field}
                      value={visibility}
                      onChange={(e) =>
                        setVisibility(e.target.value as DirectVisibility)
                      }
                    >
                      {visibilityChoices.map((o) => (
                        <option key={o.value} value={o.value}>
                          {o.label}
                        </option>
                      ))}
                    </select>
                  </label>

                  <label className="flex flex-col gap-1 text-xs text-slate-400">
                    Подпись
                    <textarea
                      className={field}
                      rows={4}
                      value={caption}
                      onChange={(e) => {
                        captionTouched.current = true;
                        setCaption(e.target.value);
                      }}
                    />
                  </label>

                  {twitchClip && (
                    <label className="flex items-start gap-2 text-sm text-slate-300">
                      <input
                        type="checkbox"
                        className="mt-1 accent-indigo-500"
                        checked={freshness}
                        onChange={(e) => setFreshness(e.target.checked)}
                      />
                      <span>
                        Опубликовать, даже если исходный клип Twitch старше
                        допустимого срока
                      </span>
                    </label>
                  )}

                  <div className="flex flex-col gap-2 rounded-lg border border-slate-600 p-3">
                    <div className="flex flex-wrap items-center gap-3">
                      <button
                        type="button"
                        className={button}
                        disabled={!variant?.eligible}
                        onClick={openPreview}
                      >
                        ▶ Посмотреть, что будет опубликовано
                      </button>
                      <span className="text-xs text-slate-400">
                        {mediaKind ? MEDIA_LABEL[mediaKind] : ""}
                      </span>
                    </div>
                    <label
                      className={`flex items-start gap-2 text-sm ${previewed.includes(reviewKey) ? "text-slate-200" : "text-slate-500"}`}
                    >
                      <input
                        type="checkbox"
                        className="mt-1 accent-indigo-500"
                        checked={reviewed}
                        disabled={!previewed.includes(reviewKey)}
                        onChange={(e) =>
                          setConfirmed(e.target.checked ? reviewKey : "")
                        }
                      />
                      <span>
                        Я посмотрел(а) именно этот файл и хочу опубликовать его
                        {previewed.includes(reviewKey)
                          ? ""
                          : " (сначала откройте просмотр)"}
                      </span>
                    </label>
                  </div>

                  <div>
                    <button
                      type="button"
                      className={`${button} bg-emerald-600 hover:bg-emerald-500`}
                      disabled={!canPublish}
                      onClick={publish}
                    >
                      {submitting ? "Отправляем…" : "Опубликовать"}
                    </button>
                  </div>
                </>
              )}
            </div>
          )}

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

          <section ref={historySection} className="flex flex-col gap-2">
            <div className="flex items-center justify-between">
              <h3 className="text-xs font-semibold uppercase tracking-widest text-slate-400">
                Публикации этого клипа
              </h3>
              <Link href="/admin/calendar" className="text-xs text-indigo-400">
                Календарь публикаций →
              </Link>
            </div>
            {publications.length === 0 && (
              <p className="text-sm text-slate-500">
                Этот клип ещё не публиковался.
              </p>
            )}
            {publications.map((p) => (
              <PublicationRow key={p.id} publication={p} onChanged={() => pollNow.current()} />
            ))}
          </section>
        </section>
      </div>
      <PreviewModal target={preview} onClose={() => setPreview(null)} />
    </>
  );
}

function PublicationRow({ publication: p, onChanged }: { publication: PublicationEvent; onChanged: () => void }) {
  const [history, setHistory] = useState<PublicationHistory[] | null>(null);
  const [open, setOpen] = useState(false);
  const [error, setError] = useState("");
  const loaded = useRef("");
  const state = p.state ?? p.status ?? "";
  // Exhausted pre-submit failures may require attention without an uncertain
  // final send. The retry endpoint still checks the durable final-send fence.
  const preflightFailed = state === "needs_attention" && p.error_code === "upload_preflight_failed";
  const [busy, setBusy] = useState(false);
  async function act(action: "reconcile" | "retry" | "publish-now") {
    setBusy(true); setError("");
    try {
      if (action === "reconcile") await reconcileTikTokPublication(p.id, p.version);
      else await actPublication(p.id, action, p.version);
      onChanged();
    } catch (e) { setError(message(e)); }
    finally { setBusy(false); }
  }

  useEffect(() => {
    const key = `${p.version}:${state}`;
    if (!open || loaded.current === key) return;
    loaded.current = key;
    fetchPublicationHistory(p.id)
      .then((h) => {
        setHistory(h);
        setError("");
      })
      .catch((e) => {
        loaded.current = "";
        setError(message(e));
      });
  }, [open, p.id, p.version, state]);

  const when = p.published_at ?? p.publish_at;
  return (
    <div className="flex flex-col gap-1 rounded-lg border border-slate-700 p-3 text-sm">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-slate-100">
          {p.account_name} · {PLATFORM_LABEL[p.platform] ?? p.platform}
        </span>
        <span
          className={
            state === "published"
              ? "text-emerald-400"
              : state === "failed"
                ? "text-red-400"
                : "text-amber-300"
          }
        >
          {publicationStateLabel(state)}
        </span>
      </div>
      {when && <p className="text-xs text-slate-500">{formatInZone(when)}</p>}
      {p.error && <p className="text-xs text-red-300">{p.error}</p>}
      <div className="flex flex-wrap gap-3 text-xs">
        <span className="text-slate-500">Публикация #{p.id}</span>
        {p.platform === "tiktok" && state === "needs_attention" && !preflightFailed && (
          <button className="text-indigo-300 underline" disabled={busy} onClick={() => void act("reconcile")}>Сверить результат без новой загрузки</button>
        )}
        {(["failed", "blocked"].includes(state) || preflightFailed) && (
          <button className="text-indigo-300 underline" disabled={busy} onClick={() => void act(preflightFailed || p.started_at ? "retry" : "publish-now")}>Повторить существующую публикацию</button>
        )}
        {p.external_url && (
          <a
            href={p.external_url}
            target="_blank"
            rel="noreferrer"
            className="text-indigo-400 underline"
          >
            Открыть публикацию ↗
          </a>
        )}
        <button
          type="button"
          className="text-indigo-300 underline"
          aria-expanded={open}
          onClick={() => setOpen((v) => !v)}
        >
          {open ? "Скрыть историю" : "История"}
        </button>
      </div>
      {preflightFailed && <p className="text-xs text-amber-300">Подготовка завершилась ошибкой до публикации. Можно повторить эту запись; сервер проверит, что отправка ещё не начиналась.</p>}
      {state === "needs_attention" && !preflightFailed && <p className="text-xs text-amber-300">Результат требует сверки. Новую загрузку создавать нельзя; проверьте аккаунт и существующую публикацию.</p>}
      {error && <p role="alert" className="text-xs text-red-400">{error}</p>}
      {open && (
        <ul className="mt-1 flex flex-col gap-1 text-xs text-slate-400">
          {error && <li className="text-red-400">{error}</li>}
          {!history && !error && <li>Загружаем историю…</li>}
          {history?.length === 0 && <li>Записей пока нет.</li>}
          {history?.map((h, i) => (
            <li key={h.id ?? i}>
              {formatInZone(h.created_at)} · {publicationStateLabel(h.state)}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
