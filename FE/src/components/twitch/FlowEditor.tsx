"use client";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import Toggle from "@/components/ui/Toggle";
import TwitchNav from "./TwitchNav";
import CapacityPanel from "./CapacityPanel";
import {
  createTwitchFlow,
  fetchAccountsList,
  fetchTwitchCategories,
  fetchTwitchFlow,
  fetchTwitchFlows,
  saveTwitchFlow,
} from "@/lib/api";
import {
  defaultTwitchFlow,
  parseSlots,
  validateTwitchFlow,
} from "@/lib/twitch-flow";
import type { TwitchFlow } from "@/types/twitch";
import type { AccountRecord } from "@/types";
const input =
  "w-full min-w-0 rounded-lg border border-slate-600 bg-slate-700 px-3 py-2 text-sm text-slate-100";
const button =
  "rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600 disabled:opacity-40";
const steps = [
  "YouTube-канал",
  "Источники и фильтры",
  "Форматы и подборки",
  "Публикация",
];
export default function FlowEditor({ flowID }: { flowID?: string }) {
  const router = useRouter();
  const [flow, setFlow] = useState<TwitchFlow>(defaultTwitchFlow);
  const [original, setOriginal] = useState<TwitchFlow | null>(null);
  const [accounts, setAccounts] = useState<AccountRecord[]>([]);
  const [existingFlows, setExistingFlows] = useState<TwitchFlow[]>([]);
  const [step, setStep] = useState(0),
    [busy, setBusy] = useState(false),
    [loading, setLoading] = useState(true);
  const [error, setError] = useState(""),
    [categoryQuery, setCategoryQuery] = useState(""),
    [categoryBusy, setCategoryBusy] = useState(false);
  const [categories, setCategories] = useState<{ id: string; name: string }[]>(
    [],
  );
  const [channelsText, setChannelsText] = useState(""),
    [shortsText, setShortsText] = useState("09:00, 12:00, 15:00, 18:00, 21:00"),
    [compilationText, setCompilationText] = useState("21:30");
  const [dirty, setDirty] = useState(false);
  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const copyID = !flowID
        ? new URLSearchParams(window.location.search).get("copy")
        : null;
      const [available, existing, currentFlows] = await Promise.all([
        fetchAccountsList(),
        flowID || copyID
          ? fetchTwitchFlow(flowID || copyID!)
          : Promise.resolve(null),
        fetchTwitchFlows(),
      ]);
      setAccounts(available ?? []);
      setExistingFlows(currentFlows);
      if (existing) {
        setOriginal(copyID ? null : existing);
        const value = copyID
          ? {
              ...existing,
              id: "",
              version: 0,
              name: `${existing.name} — копия`,
              account_id: 0,
              youtube_channel_id: "",
              enabled: false,
              archived: false,
              auto_publish: false,
              work_started_at: undefined,
              excluded_source_ids: [],
              ...(existing.legacy_length
                ? {
                    legacy_length: false,
                    compilation_min_duration_sec: 480,
                    compilation_max_duration_sec: 720,
                    compilation_min_clips: 3,
                    compilation_max_clips: 60,
                  }
                : {}),
            }
          : existing;
        setFlow(value);
        setChannelsText((value.channel_inputs || []).join("\n"));
        setShortsText((value.shorts_slots || []).join(", "));
        setCompilationText((value.compilation_slots || []).join(", "));
      }
      setDirty(false);
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      setLoading(false);
    }
  }, [flowID]);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    if (!dirty) return;
    const warn = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);
  function change<K extends keyof TwitchFlow>(field: K, value: TwitchFlow[K]) {
    setFlow((previous) => ({ ...previous, [field]: value }));
    setDirty(true);
  }
  const readyAccounts = accounts.filter(
    (account) =>
      account.platform === "youtube" &&
      account.connection_state === "connected" &&
      account.remote_account_id,
  );
  function selectAccount(accountID: number) {
    const account = readyAccounts.find((item) => item.id === accountID);
    const policy = existingFlows.find(
      (item) => item.youtube_channel_id === account?.remote_account_id,
    );
    setFlow((previous) => ({
      ...previous,
      account_id: accountID,
      ...(policy
        ? {
            timezone: policy.timezone,
            daily_shorts: policy.daily_shorts,
            daily_compilations: policy.daily_compilations,
            schedule_horizon_hours: policy.schedule_horizon_hours,
          }
        : {}),
    }));
    setDirty(true);
  }
  const candidate = (): TwitchFlow => ({
    ...flow,
    channel_inputs: channelsText
      .split(/[\s,;]+/)
      .map((value) => value.trim())
      .filter(Boolean),
    shorts_slots: parseSlots(shortsText),
    compilation_slots: parseSlots(compilationText),
  });
  function next() {
    const issue = validateTwitchFlow(candidate(), step);
    if (issue) {
      setError(issue);
      return;
    }
    setError("");
    setStep((previous) => Math.min(3, previous + 1));
  }
  async function save() {
    const value = candidate(),
      issue = validateTwitchFlow(value);
    if (issue) {
      setError(issue);
      return;
    }
    const stopping =
      !!flowID &&
      !!original &&
      value.account_id === original.account_id &&
      !(value.enabled && !original.enabled) &&
      !(value.auto_publish && !original.auto_publish) &&
      ((!value.enabled && !value.auto_publish) ||
        (original.enabled && !value.enabled) ||
        (original.auto_publish && !value.auto_publish));
    if (
      !stopping &&
      !readyAccounts.some((account) => account.id === value.account_id)
    ) {
      setError(
        "Назначение недоступно. Подключите и подтвердите YouTube-канал в разделе Accounts.",
      );
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = flowID
        ? await saveTwitchFlow(value)
        : await createTwitchFlow(value);
      setDirty(false);
      router.push(`/admin/twitch?flow=${encodeURIComponent(result.id)}`);
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      setBusy(false);
    }
  }
  async function searchCategories() {
    if (!categoryQuery.trim()) return;
    setCategoryBusy(true);
    setError("");
    try {
      setCategories((await fetchTwitchCategories(categoryQuery.trim())) || []);
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      setCategoryBusy(false);
    }
  }
  const numberField = (
    field: keyof TwitchFlow,
    label: string,
    min: number,
    max: number,
    hint?: string,
    divisor = 1,
  ) => (
    <label className="flex flex-col gap-1 text-sm text-slate-300" key={field}>
      {label}
      <input
        className={input}
        type="number"
        required
        min={min}
        max={max}
        step={divisor === 1 ? 1 : 0.5}
        value={
          typeof flow[field] === "number" ? Number(flow[field]) / divisor : ""
        }
        onChange={(event) =>
          change(field, (Number(event.target.value) * divisor) as never)
        }
      />
      {hint && <span className="text-xs text-slate-400">{hint}</span>}
    </label>
  );
  return (
    <div className="flex flex-col gap-5 p-4 sm:p-6">
      <div className="flex items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">
          {flowID ? "Настройки потока" : "Новый поток публикаций"}
        </h1>
        <Link className="text-sm text-indigo-400" href="/admin/twitch">
          ← Все потоки
        </Link>
      </div>
      <TwitchNav />
      <p className="text-sm text-slate-400">
        Один поток собирает клипы по своим правилам и выпускает их на одном
        YouTube-канале. Исходные файлы доступны нескольким потокам.
      </p>
      <ol className="grid grid-cols-2 gap-2 lg:grid-cols-4">
        {steps.map((label, index) => (
          <li key={label}>
            <button
              className={`w-full rounded-lg border px-3 py-3 text-left text-sm ${step === index ? "border-indigo-500 bg-indigo-600" : "border-slate-700 bg-slate-800"}`}
              disabled={loading || busy || (!flowID && index > step)}
              onClick={() => {
                setStep(index);
                setError("");
              }}
            >
              {index + 1}. {label}
            </button>
          </li>
        ))}
      </ol>
      {error && (
        <p
          role="alert"
          className="rounded-lg border border-red-800 p-3 text-sm text-red-300"
        >
          {error}
        </p>
      )}
      {loading ? (
        <p className="text-slate-400">Загружаем настройки…</p>
      ) : (
        <form
          className="flex max-w-4xl flex-col gap-5 rounded-xl border border-slate-700 bg-slate-800 p-5"
          onSubmit={(event) => {
            event.preventDefault();
            if (step < 3) next();
            else void save();
          }}
        >
          <fieldset disabled={busy} className="flex flex-col gap-5">
            {step === 0 && (
              <>
                <label className="flex flex-col gap-1 text-sm">
                  Название потока
                  <input
                    className={input}
                    required
                    maxLength={100}
                    value={flow.name}
                    placeholder="Например, русские клипы по Dota 2"
                    onChange={(event) => change("name", event.target.value)}
                  />
                </label>
                <label className="flex flex-col gap-1 text-sm">
                  YouTube-канал
                  <select
                    className={input}
                    required
                    value={flow.account_id}
                    disabled={!!flowID && !!flow.work_started_at}
                    onChange={(event) =>
                      selectAccount(Number(event.target.value))
                    }
                  >
                    <option value={0}>Выберите подключённый канал</option>
                    {flow.account_id > 0 &&
                      !readyAccounts.some(
                        (account) => account.id === flow.account_id,
                      ) && (
                        <option value={flow.account_id}>
                          Назначение недоступно · аккаунт {flow.account_id}
                        </option>
                      )}
                    {readyAccounts.map((account) => (
                      <option value={account.id} key={account.id}>
                        {account.remote_account_name || account.name} ·{" "}
                        {account.remote_account_id}
                      </option>
                    ))}
                  </select>
                </label>
                <p className="text-xs text-slate-400">
                  Подтвердите канал в{" "}
                  <Link href="/admin/accounts" className="text-indigo-400">
                    Accounts
                  </Link>
                  . Чтобы направить работающий поток в другой YouTube-канал,
                  создайте копию потока.
                </p>
                {flowID && (
                  <Link
                    href={`/admin/twitch/new?copy=${encodeURIComponent(flowID)}`}
                    className="text-sm text-indigo-400"
                  >
                    Создать копию для другого канала →
                  </Link>
                )}
              </>
            )}
            {step === 1 && (
              <>
                <label className="flex flex-col gap-1 text-sm">
                  Откуда собирать клипы
                  <select
                    className={input}
                    value={flow.source_mode}
                    onChange={(event) =>
                      change(
                        "source_mode",
                        event.target.value as TwitchFlow["source_mode"],
                      )
                    }
                  >
                    <option value="channels">Список Twitch-каналов</option>
                    <option value="category">Категория по всему Twitch</option>
                  </select>
                </label>
                {flow.source_mode === "channels" ? (
                  <label className="flex flex-col gap-1 text-sm">
                    Ссылки или логины Twitch-каналов
                    <textarea
                      className={input}
                      rows={4}
                      value={channelsText}
                      placeholder={"https://twitch.tv/channel\nдругой_канал"}
                      onChange={(event) => {
                        setChannelsText(event.target.value);
                        setDirty(true);
                      }}
                    />
                    <span className="text-xs text-slate-400">
                      Каждый канал с новой строки. Ссылки проверяются и
                      преобразуются в постоянные Twitch ID.
                    </span>
                    {!channelsText && flow.broadcaster_ids.length > 0 && (
                      <span className="text-xs text-slate-400">
                        Сохранено каналов: {flow.broadcaster_ids.length}.
                        Оставьте поле пустым, чтобы сохранить текущий список.
                      </span>
                    )}
                  </label>
                ) : (
                  <div className="flex flex-col gap-2">
                    <label className="text-sm" htmlFor="category-search">
                      Поиск категории
                    </label>
                    <div className="flex gap-2">
                      <input
                        id="category-search"
                        className={input}
                        value={categoryQuery}
                        placeholder="Dota 2, Just Chatting…"
                        onChange={(event) =>
                          setCategoryQuery(event.target.value)
                        }
                      />
                      <button
                        type="button"
                        className={button}
                        disabled={categoryBusy || !categoryQuery.trim()}
                        onClick={() => void searchCategories()}
                      >
                        {categoryBusy ? "Ищем…" : "Найти"}
                      </button>
                    </div>
                    {flow.category_id && (
                      <p className="text-sm text-emerald-300">
                        Выбрано: {flow.category_name || flow.category_id}
                      </p>
                    )}
                    {categories.length > 0 && (
                      <select
                        aria-label="Результаты поиска категории"
                        className={input}
                        value={flow.category_id || ""}
                        onChange={(event) => {
                          const selected = categories.find(
                            (item) => item.id === event.target.value,
                          );
                          change("category_id", selected?.id || "");
                          change("category_name", selected?.name || "");
                        }}
                      >
                        <option value="">Выберите категорию</option>
                        {categories.map((item) => (
                          <option key={item.id} value={item.id}>
                            {item.name}
                          </option>
                        ))}
                      </select>
                    )}
                    <p className="text-xs text-slate-400">
                      Collector ищет популярные клипы всей категории, а не
                      только вашего списка каналов.
                    </p>
                  </div>
                )}
                <label className="flex flex-col gap-1 text-sm">
                  Язык вещания
                  <select
                    className={input}
                    value={flow.language || ""}
                    onChange={(event) => change("language", event.target.value)}
                  >
                    <option value="">Любой язык</option>
                    {[
                      ["ru", "Русский"],
                      ["en", "Английский"],
                      ["es", "Испанский"],
                      ["de", "Немецкий"],
                      ["fr", "Французский"],
                      ["pt", "Португальский"],
                      ["uk", "Украинский"],
                      ["ja", "Японский"],
                      ["ko", "Корейский"],
                    ].map(([value, label]) => (
                      <option value={value} key={value}>
                        {label}
                      </option>
                    ))}
                  </select>
                  <span className="text-xs text-slate-400">
                    Язык клипа по данным Twitch — язык вещания в момент записи.
                    Речь может отличаться.
                  </span>
                </label>
                <div className="grid gap-4 sm:grid-cols-2">
                  {numberField(
                    "poll_interval_minutes",
                    "Проверять каждые, минут",
                    1,
                    10080,
                  )}
                  {numberField(
                    "window_hours",
                    "Окно свежих клипов, часов",
                    1,
                    720,
                  )}
                  {numberField(
                    "min_views",
                    "Минимум просмотров",
                    0,
                    Number.MAX_SAFE_INTEGER,
                  )}
                  {numberField(
                    "min_age_minutes",
                    "Минимальный возраст, минут",
                    0,
                    Number.MAX_SAFE_INTEGER,
                  )}
                  {numberField(
                    "min_duration_sec",
                    "Минимальная длина клипа, секунд",
                    1,
                    60,
                  )}
                  {numberField(
                    "max_duration_sec",
                    "Максимальная длина клипа, секунд",
                    1,
                    60,
                  )}
                </div>
              </>
            )}
            {step === 2 && (
              <>
                <Toggle
                  label="Создавать Shorts"
                  checked={flow.shorts_enabled}
                  onChange={(value) => change("shorts_enabled", value)}
                />
                <Toggle
                  label="Создавать подборки"
                  checked={flow.compilations_enabled}
                  onChange={(value) => change("compilations_enabled", value)}
                />
                <p className="text-xs text-slate-400">
                  Подборки используют горизонтальные исходники и не требуют
                  готового Short.
                </p>
                {flow.compilations_enabled && (
                  <>
                    {flow.legacy_length && (
                      <div className="rounded-lg border border-amber-800 p-3 text-sm text-amber-200">
                        Этот поток перенесён из прежних настроек: длина пока
                        определяется количеством клипов.
                        <button
                          type="button"
                          className="mt-2 block text-indigo-300"
                          onClick={() => {
                            change("legacy_length", false);
                            change("compilation_min_duration_sec", 480);
                            change("compilation_max_duration_sec", 720);
                          }}
                        >
                          Перейти на подборки 8–12 минут
                        </button>
                      </div>
                    )}
                    <div className="grid gap-4 sm:grid-cols-2">
                      {!flow.legacy_length && (
                        <>
                          {numberField(
                            "compilation_min_duration_sec",
                            "Минимальная длина, минут",
                            0.5,
                            60,
                            "Учитываются клипы и секундные перебивки.",
                            60,
                          )}
                          {numberField(
                            "compilation_max_duration_sec",
                            "Максимальная длина, минут",
                            0.5,
                            60,
                            undefined,
                            60,
                          )}
                        </>
                      )}
                      {!channelsText &&
                        flow.id === "default" &&
                        flow.legacy_length &&
                        !flow.broadcaster_ids.length && (
                          <span className="text-xs text-amber-200">
                            Основной поток использует сохранённый каталог
                            Twitch-каналов. Пустое поле сохраняет этот набор.
                            При переходе на новые правила длины или создании
                            копии укажите каналы явно.
                          </span>
                        )}
                      {numberField(
                        "compilation_min_clips",
                        "Минимум клипов",
                        3,
                        60,
                      )}
                      {numberField(
                        "compilation_max_clips",
                        "Максимум клипов",
                        3,
                        60,
                      )}
                      {numberField(
                        "compilation_hour",
                        "Готовить после часа",
                        0,
                        23,
                        "Не больше одного автоматического черновика за сутки на поток.",
                      )}
                    </div>
                    <Toggle
                      label="Балансировать время стримеров"
                      checked={flow.balance_by_time}
                      onChange={(value) => change("balance_by_time", value)}
                    />
                    <p className="text-xs text-slate-400">
                      Сначала более популярные моменты; следующий клип берётся у
                      стримера с наименьшим временем в подборке. Баланс
                      приблизительный. Если материала не хватает, черновик ждёт
                      новых клипов.
                    </p>
                  </>
                )}
              </>
            )}
            {step === 3 && (
              <>
                {flowID && !flow.archived && <CapacityPanel flowID={flowID} timezone={original?.timezone || "Asia/Tomsk"} />}
                <Toggle
                  label="Включить поток"
                  checked={flow.enabled}
                  onChange={(value) => change("enabled", value)}
                />
                <p className="text-xs text-slate-400">
                  Пауза останавливает новые проверки и начало публикаций. Уже
                  начатые загрузки YouTube завершатся.
                </p>
                <Toggle
                  label="Публиковать автоматически"
                  checked={flow.auto_publish}
                  onChange={(value) => change("auto_publish", value)}
                />
                <p
                  className={`text-sm ${flow.auto_publish ? "text-amber-200" : "text-slate-400"}`}
                >
                  {flow.auto_publish
                    ? "Новые автоматически созданные выпуски публикуются без просмотра в ближайший свободный слот. Старые ручные черновики и изменённые версии требуют одобрения."
                    : "Посмотрите и одобрите готовую версию в редакторе перед публикацией. Это стандартный режим."}
                </p>
                <label className="flex flex-col gap-1 text-sm">
                  Часовой пояс YouTube-канала
                  <input
                    className={input}
                    value={flow.timezone}
                    onChange={(event) => change("timezone", event.target.value)}
                  />
                  <span className="text-xs text-slate-400">
                    Один часовой пояс и суммарные лимиты применяются ко всем
                    потокам этого YouTube-канала.
                  </span>
                </label>
                <div className="grid gap-4 sm:grid-cols-2">
                  {numberField(
                    "schedule_horizon_hours",
                    "Заполнять календарь на ближайшие, часов",
                    1,
                    168,
                    "Скользящий горизонт от текущего времени. Общий для всех потоков этого YouTube-канала; изменение повлияет на них все.",
                  )}
                  {numberField(
                    "max_source_age_hours",
                    "Максимальный возраст клипа на дату публикации, часов",
                    1,
                    720,
                    "Считается от создания на Twitch, а не скачивания. В подборке проверяется каждый исходник. Окно поиска не продлевает актуальность.",
                  )}
                  {flow.shorts_enabled && (
                    <label className="flex flex-col gap-1 text-sm">
                      Времена Shorts
                      <input
                        className={input}
                        value={shortsText}
                        onChange={(event) => {
                          setShortsText(event.target.value);
                          setDirty(true);
                        }}
                        placeholder="09:00, 12:00"
                      />
                    </label>
                  )}
                  {flow.compilations_enabled && (
                    <label className="flex flex-col gap-1 text-sm">
                      Время подборок
                      <input
                        className={input}
                        value={compilationText}
                        onChange={(event) => {
                          setCompilationText(event.target.value);
                          setDirty(true);
                        }}
                        placeholder="21:30"
                      />
                    </label>
                  )}
                  {numberField(
                    "daily_shorts",
                    "Shorts на YouTube-канал в сутки",
                    1,
                    100,
                  )}
                  {numberField(
                    "daily_compilations",
                    "Подборок на YouTube-канал в сутки",
                    1,
                    10,
                  )}
                </div>
                <p className="text-xs text-slate-400">
                  Времена: ЧЧ:ММ через запятую. Исчерпанный лимит или занятый
                  слот переводят выпуск в ожидание. Выключение автоматики
                  удерживает ещё не начатые автоматические публикации.
                </p>
              </>
            )}
            <div className="flex items-center justify-between gap-3">
              <button
                type="button"
                className={button}
                disabled={step === 0}
                onClick={() => setStep((previous) => previous - 1)}
              >
                Назад
              </button>
              <button
                className="rounded-lg bg-indigo-600 px-4 py-2 text-sm hover:bg-indigo-500 disabled:opacity-40"
                disabled={busy}
              >
                {busy ? "Сохраняем…" : step < 3 ? "Далее" : "Сохранить поток"}
              </button>
            </div>
          </fieldset>
        </form>
      )}
    </div>
  );
}
