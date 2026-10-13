"use client";

import { useEffect, useState } from "react";
import {
  browserHelpTikTok,
  checkTikTok,
  connectTikTok,
  disconnectTikTok,
  fetchAccountsList,
  fetchTikTokStatus,
} from "@/lib/api";
import type { AccountRecord, TikTokConnection } from "@/types";

const button =
  "rounded-md border border-slate-600 bg-slate-700 px-3 py-2 text-xs disabled:opacity-40 hover:bg-slate-600";
const states: Record<string, string> = {
  connected: "Подключён — личность аккаунта проверена в TikTok",
  pairing: "Ждёт подключения через расширение браузера",
  needs_reauth: "Сессия истекла — подключите аккаунт заново",
  challenge: "TikTok просит подтверждение — откройте браузер исполнителя",
  disconnected: "Не подключён",
  auth_required: "Нужно повторно войти в TikTok",
};

export default function TikTokConnectionPanel({
  account,
  onUpdated,
}: {
  account: AccountRecord;
  onUpdated: (account: AccountRecord) => void;
}) {
  const [connection, setConnection] = useState<TikTokConnection | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [help, setHelp] = useState<{ url: string; expires_at: string } | null>(
    null,
  );
  const [now, setNow] = useState(0);
  const expiry = connection?.expires_at || connection?.pairing_expires_at;
  const expired = !!expiry && now >= new Date(expiry).getTime();
  const status =
    connection?.status || account.connection_state || "disconnected";
  useEffect(() => {
    if (!connection?.pairing_token || !expiry) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [connection?.pairing_token, expiry]);

  async function action(
    kind: "connect" | "status" | "check" | "disconnect" | "help",
  ) {
    if (
      kind === "disconnect" &&
      !window.confirm(
        "Отключить TikTok? Ещё не начатые публикации будут ждать повторного подключения.",
      )
    )
      return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      if (kind === "help") {
        const help = await browserHelpTikTok(account.id);
        const url = new URL(help.help_url, window.location.origin);
        if (
          url.origin !== window.location.origin ||
          !url.pathname.startsWith("/api/tiktok/browser-help/")
        )
          throw new Error("Получен неподходящий адрес браузера исполнителя.");
        setHelp({ url: url.href, expires_at: help.expires_at });
        setNotice(
          "Ссылка на браузер исполнителя готова ниже. Она действует 10 минут; после входа нажмите «Проверить сессию».",
        );
        return;
      }
      const result = await (kind === "connect"
        ? connectTikTok(account)
        : kind === "check"
          ? checkTikTok(account.id)
          : kind === "disconnect"
            ? disconnectTikTok(account)
            : fetchTikTokStatus(account.id));
      setConnection((previous) =>
        result.status === "pairing" &&
        previous?.pairing_token &&
        !result.pairing_token
          ? { ...previous, ...result }
          : result,
      );
      setNow(Date.now());
      const updated = (await fetchAccountsList()).find(
        (item) => item.id === account.id,
      );
      if (updated) onUpdated(updated);
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mt-2 space-y-3 rounded-lg border border-slate-600 p-3 text-xs">
      <p
        className={
          status === "connected" ? "text-emerald-300" : "text-amber-200"
        }
      >
        {states[status] || `Состояние: ${status}`}
      </p>
      <p className="text-slate-400">
        Сохраните аккаунт, войдите в TikTok в Chrome или Edge и передайте сессию
        через расширение Terex. Проверенная личность должна совпадать с
        назначением аккаунта.
      </p>
      <details className="text-slate-400">
        <summary className="cursor-pointer text-indigo-300">Как подключить TikTok</summary>
        <ol className="mt-2 list-decimal space-y-1 pl-5">
          <li>Установите расширение Terex из папки extension репозитория uploader: в Chrome/Edge откройте страницу расширений, включите режим разработчика и загрузите распакованную папку.</li>
          <li>Откройте tiktok.com и войдите именно в аккаунт, который хотите подключить.</li>
          <li>Получите здесь одноразовый код, откройте расширение на вкладке TikTok и укажите адрес Terex и код. После передачи обновите состояние и проверьте подтверждённый TikTok ID.</li>
          <li>Если TikTok запросил проверку или сессия истекла, используйте переподключение либо защищённый браузер исполнителя. Назначение аккаунта не меняется автоматически.</li>
        </ol>
      </details>
      {(connection?.remote_account_id || account.remote_account_id) && (
        <p className="text-slate-300">
          TikTok ID:{" "}
          {connection?.remote_account_id || account.remote_account_id} · @
          {connection?.username ||
            account.remote_account_name ||
            "проверенный аккаунт"}
          {connection?.session_version != null &&
            ` · сессия ${connection.session_version}`}
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        <button
          className={button}
          disabled={busy}
          onClick={() => void action("connect")}
        >
          {status === "connected" || status === "needs_reauth"
            ? "Подключить заново"
            : "Получить код подключения"}
        </button>
        <button
          className={button}
          disabled={busy}
          onClick={() => void action("status")}
        >
          Обновить состояние
        </button>
        <button
          className={button}
          disabled={busy}
          onClick={() => void action("check")}
        >
          Проверить сессию
        </button>
        <button
          className={button}
          disabled={busy}
          onClick={() => void action("help")}
        >
          Открыть браузер исполнителя
        </button>
        {status !== "disconnected" && (
          <button
            className={`${button} text-red-300`}
            disabled={busy}
            onClick={() => void action("disconnect")}
          >
            Отключить
          </button>
        )}
      </div>
      {connection?.pairing_token && (
        <div className="space-y-2 rounded-md bg-slate-900 p-3">
          {expired ? (
            <p className="text-amber-200">
              Код истёк. Получите новый код подключения.
            </p>
          ) : (
            <>
              <p className="text-slate-300">
                Вставьте код в расширение Terex на вкладке TikTok. Код действует
                10 минут и используется один раз.
              </p>
              <div className="flex items-center gap-2">
                <code className="min-w-0 flex-1 break-all text-indigo-200">
                  {connection.pairing_token}
                </code>
                <button
                  type="button"
                  className={button}
                  onClick={() =>
                    navigator.clipboard
                      .writeText(connection.pairing_token!)
                      .then(() => setNotice("Код скопирован."))
                      .catch(() =>
                        setError(
                          "Не удалось скопировать. Выделите код вручную.",
                        ),
                      )
                  }
                >
                  Копировать
                </button>
              </div>
              {connection.payload_url && (
                <p className="break-all text-slate-400">
                  Адрес подключения для расширения: {connection.payload_url}
                </p>
              )}
              {expiry && (
                <p className="text-slate-400">
                  До {new Date(expiry).toLocaleTimeString()} · осталось{" "}
                  {Math.max(
                    0,
                    Math.ceil((new Date(expiry).getTime() - now) / 60000),
                  )}{" "}
                  мин. После передачи нажмите «Обновить состояние».
                </p>
              )}
            </>
          )}
        </div>
      )}
      {(connection?.error || error) && (
        <p role="alert" className="text-red-300">
          {error || connection?.error}
        </p>
      )}
      {notice && (
        <p role="status" className="text-indigo-200">
          {notice}
        </p>
      )}
      {help && (
        <a
          href={help.url}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-block text-indigo-300 underline"
        >
          Открыть защищённый браузер TikTok (до{" "}
          {new Date(help.expires_at).toLocaleTimeString()}) →
        </a>
      )}
    </div>
  );
}
