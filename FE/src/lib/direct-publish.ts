import type {
  AccountRecord,
  DirectMediaKind,
  DirectPlatform,
  DirectPublishRequest,
  DirectVisibility,
} from "@/types";

export interface KeyValueStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

const STORAGE_PREFIX = "terex.direct-publish.v1.";

export function newRequestID(): string {
  const c = globalThis.crypto;
  if (c && typeof c.randomUUID === "function") return c.randomUUID();
  // randomUUID exists only in secure contexts; plain-http staging still needs a UUID.
  const bytes = new Uint8Array(16);
  if (c && typeof c.getRandomValues === "function") c.getRandomValues(bytes);
  else for (let i = 0; i < 16; i++) bytes[i] = Math.floor(Math.random() * 256);
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function validRequest(value: unknown): value is DirectPublishRequest {
  const r = value as Partial<DirectPublishRequest> | null;
  return (
    !!r &&
    typeof r === "object" &&
    typeof r.request_id === "string" &&
    UUID.test(r.request_id) &&
    Number.isInteger(r.revision_id) &&
    Number.isInteger(r.expected_version) &&
    (r.media_kind === "rendered" || r.media_kind === "source") &&
    Number.isInteger(r.account_id) &&
    (r.platform === "youtube" || r.platform === "tiktok") &&
    typeof r.caption === "string" &&
    (r.visibility === "private" ||
      r.visibility === "unlisted" ||
      r.visibility === "public") &&
    r.reviewed === true &&
    (r.freshness_override === undefined ||
      typeof r.freshness_override === "boolean")
  );
}

/** The unconfirmed request of a clip, exactly as it was first sent. */
export function loadPendingRequest(
  storage: KeyValueStorage | null | undefined,
  clipID: string,
): DirectPublishRequest | null {
  if (!storage) return null;
  try {
    const raw = storage.getItem(STORAGE_PREFIX + clipID);
    const value: unknown = raw ? JSON.parse(raw) : null;
    return validRequest(value) ? value : null;
  } catch {
    return null;
  }
}

export function savePendingRequest(
  storage: KeyValueStorage | null | undefined,
  clipID: string,
  request: DirectPublishRequest,
) {
  try {
    storage?.setItem(STORAGE_PREFIX + clipID, JSON.stringify(request));
  } catch {
    // Without storage the request stays valid only until the page is closed.
  }
}

export function clearPendingRequest(
  storage: KeyValueStorage | null | undefined,
  clipID: string,
) {
  try {
    storage?.removeItem(STORAGE_PREFIX + clipID);
  } catch {
    // Nothing to clean up.
  }
}

/**
 * True only when the server answered and certainly did not create anything.
 * Network failures, timeouts, 5xx and throttling may hide an accepted request,
 * so they must be replayed with the same request_id.
 */
export function isDefiniteRejection(error: unknown): boolean {
  const status = (error as { status?: unknown } | null)?.status;
  return (
    typeof status === "number" &&
    status >= 400 &&
    status < 500 &&
    ![408, 425, 429].includes(status)
  );
}

export function accountReadiness(account: AccountRecord): {
  ready: boolean;
  reason: string;
} {
  if (account.platform !== "youtube" && account.platform !== "tiktok")
    return { ready: false, reason: "платформа не поддерживается" };
  if (account.connection_state !== "connected")
    return { ready: false, reason: "аккаунт не подключён" };
  if (account.platform === "tiktok" && !((account.session_version ?? 0) > 0))
    return { ready: false, reason: "нужно заново войти в TikTok" };
  return { ready: true, reason: "" };
}

export function visibilityOptions(
  platform: DirectPlatform,
): { value: DirectVisibility; label: string }[] {
  const all: { value: DirectVisibility; label: string }[] = [
    { value: "private", label: "Только для меня" },
    { value: "unlisted", label: "Доступно по ссылке" },
    { value: "public", label: "Для всех" },
  ];
  return platform === "tiktok" ? all.filter((o) => o.value !== "unlisted") : all;
}

export const MEDIA_LABEL: Record<DirectMediaKind, string> = {
  rendered: "Готовый клип",
  source: "Оригинал целиком",
};

export const MEDIA_EXPLANATION: Record<DirectMediaKind, string> = {
  rendered:
    "Нарезанный клип в том виде, в каком он готов к выпуску: с вашими правками и оформлением.",
  source:
    "Всё скачанное исходное видео целиком, как оно было загружено, без нарезки, субтитров, баннера и других эффектов.",
};

const TERMINAL = ["published", "failed", "cancelled", "canceled"];
export const isTerminalPublication = (state: string) =>
  TERMINAL.includes(state);

const STATE_LABEL: Record<string, string> = {
  scheduled: "В очереди на отправку",
  queued: "В очереди на отправку",
  waiting_worker: "Ждёт свободного исполнителя",
  uploading: "Загружается",
  uploaded: "Загружено, идёт обработка",
  processing: "Обрабатывается площадкой",
  publishing: "Публикуется",
  verifying: "Проверяется результат",
  published: "Опубликовано",
  failed: "Ошибка публикации",
  retry_wait: "Повторная попытка позже",
  needs_attention: "Нужна проверка",
  blocked: "Остановлено",
  on_hold: "Приостановлено",
  cancel_pending: "Отменяется",
  cancelled: "Отменено",
  canceled: "Отменено",
};
export const publicationStateLabel = (state: string) =>
  STATE_LABEL[state] ?? state;

/** A server reason may be a machine code; never show those to the operator. */
export function readableReason(reason: string | undefined): string {
  if (!reason || /^[a-z0-9_.:-]+$/.test(reason)) return "";
  return reason;
}
