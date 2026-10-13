"use client";

import Link from "next/link";
import type { AccountRecord } from "@/types";
import type { TwitchDestination, TwitchFlow } from "@/types/twitch";
import { flowDestinations, parseSlots } from "@/lib/twitch-flow";

const input =
  "w-full min-w-0 rounded-lg border border-slate-600 bg-slate-700 px-3 py-2 text-sm text-slate-100";

export default function DestinationsEditor({
  flow,
  original,
  accounts,
  existingFlows = [],
  settings = false,
  onChange,
}: {
  flow: TwitchFlow;
  original?: TwitchFlow | null;
  accounts: AccountRecord[];
  existingFlows?: TwitchFlow[];
  settings?: boolean;
  onChange: (destinations: TwitchDestination[]) => void;
}) {
  const destinations = flowDestinations(flow);
  function update(
    provider: TwitchDestination["provider"],
    values: Partial<TwitchDestination>,
  ) {
    onChange(
      destinations.map((item) =>
        item.provider === provider ? { ...item, ...values } : item,
      ),
    );
  }
  return (
    <div className="space-y-4">
      <p className="text-sm text-slate-400">
        Один исходник и вертикальный рендер могут использоваться в YouTube и
        TikTok. У каждой платформы своё расписание, лимит и состояние
        публикации.
      </p>
      {!settings && (
        <div className="flex flex-wrap gap-4">
          {(["youtube", "tiktok"] as const).map((provider) => (
            <label className="flex items-center gap-2 text-sm" key={provider}>
              <input
                type="checkbox"
                checked={destinations.some(
                  (item) => item.provider === provider,
                )}
                onChange={(event) =>
                  onChange(
                    event.target.checked
                      ? [
                          ...destinations,
                          {
                            provider,
                            account_id: 0,
                            enabled: true,
                            auto_publish: false,
                            timezone: flow.timezone || "Asia/Tomsk",
                            shorts_slots:
                              provider === "tiktok"
                                ? ["10:00", "16:00", "21:00"]
                                : [...flow.shorts_slots],
                            daily_shorts:
                              provider === "tiktok" ? 3 : flow.daily_shorts,
                            schedule_horizon_hours:
                              provider === "tiktok"
                                ? 24
                                : flow.schedule_horizon_hours,
                            visibility: provider === "tiktok" ? "" : "public",
                          },
                        ]
                      : destinations.filter(
                          (item) => item.provider !== provider,
                        ),
                  )
                }
              />
              {provider === "youtube" ? "YouTube" : "TikTok"}
            </label>
          ))}
        </div>
      )}
      {destinations.map((destination) => {
        const ready = accounts.filter(
          (item) =>
            item.platform === destination.provider &&
            item.connection_state === "connected" &&
            item.remote_account_id,
        );
        const selected = accounts.find(
          (item) => item.id === destination.account_id,
        );
        const locked =
          !!original?.work_started_at &&
          flowDestinations(original).some(
            (item) => item.provider === destination.provider,
          );
        const label = destination.provider === "youtube" ? "YouTube" : "TikTok";
        return (
          <section
            key={destination.provider}
            className="space-y-3 rounded-lg border border-slate-600 p-4"
          >
            <h3 className="font-semibold">
              {label}
              {settings &&
                ` · ${selected?.remote_account_name || selected?.name || "аккаунт не выбран"}`}
            </h3>
            {!settings && (
              <label className="block space-y-1 text-sm">
                <span>Подключённый аккаунт</span>
                <select
                  className={input}
                  value={destination.account_id}
                  disabled={locked}
                  onChange={(event) => {
                    const account = ready.find(
                      (item) => item.id === Number(event.target.value),
                    );
                    const shared = existingFlows.flatMap(flowDestinations).find((item) => item.provider === destination.provider && !!account?.remote_account_id && item.remote_account_id === account.remote_account_id);
                    update(destination.provider, {
                      account_id: Number(event.target.value),
                      remote_account_id: account?.remote_account_id,
                      ...(shared ? { timezone: shared.timezone, daily_shorts: shared.daily_shorts, schedule_horizon_hours: shared.schedule_horizon_hours } : {}),
                    });
                  }}
                >
                  <option value={0}>Выберите подтверждённый аккаунт</option>
                  {destination.account_id > 0 &&
                    !ready.some(
                      (item) => item.id === destination.account_id,
                    ) && (
                      <option value={destination.account_id}>
                        Аккаунт {destination.account_id} недоступен —
                        переподключите
                      </option>
                    )}
                  {ready.map((item) => (
                    <option value={item.id} key={item.id}>
                      {item.remote_account_name || item.name} ·{" "}
                      {item.remote_account_id}
                    </option>
                  ))}
                </select>
                {locked && (
                  <span className="text-xs text-slate-400">
                    Для другого аккаунта создайте копию потока. Подключить
                    вторую платформу можно отдельно.
                  </span>
                )}
              </label>
            )}
            {settings && (
              <>
                <div className="flex flex-wrap gap-4 text-sm">
                  <label className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      checked={destination.enabled}
                      onChange={(event) =>
                        update(destination.provider, {
                          enabled: event.target.checked,
                        })
                      }
                    />
                    Включить {label}
                  </label>
                  <label className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      checked={destination.auto_publish}
                      disabled={destination.provider === "tiktok" && !destination.visibility}
                      onChange={(event) =>
                        update(destination.provider, {
                          auto_publish: event.target.checked,
                        })
                      }
                    />
                    Публиковать автоматически
                  </label>
                </div>
                <div className="grid gap-3 sm:grid-cols-2">
                  <label className="space-y-1 text-sm">
                    Время вертикальных клипов
                    <input
                      className={input}
                      defaultValue={destination.shorts_slots.join(", ")}
                      onChange={(event) =>
                        update(destination.provider, {
                          shorts_slots: parseSlots(event.target.value),
                        })
                      }
                      placeholder="10:00, 16:00, 21:00"
                    />
                  </label>
                  <label className="space-y-1 text-sm">
                    Часовой пояс
                    <input
                      className={input}
                      value={destination.timezone}
                      onChange={(event) =>
                        update(destination.provider, {
                          timezone: event.target.value,
                        })
                      }
                    />
                  </label>
                  <label className="space-y-1 text-sm">
                    Лимит клипов в сутки
                    <input
                      className={input}
                      type="number"
                      min={1}
                      max={100}
                      value={destination.daily_shorts}
                      onChange={(event) =>
                        update(destination.provider, {
                          daily_shorts: Number(event.target.value),
                        })
                      }
                    />
                  </label>
                  <label className="space-y-1 text-sm">
                    Горизонт календаря, часов
                    <input
                      className={input}
                      type="number"
                      min={1}
                      max={168}
                      value={destination.schedule_horizon_hours}
                      onChange={(event) =>
                        update(destination.provider, {
                          schedule_horizon_hours: Number(event.target.value),
                        })
                      }
                    />
                  </label>
                  <label className="space-y-1 text-sm">
                    Видимость
                    <select
                      className={input}
                      value={destination.visibility}
                      onChange={(event) =>
                        update(destination.provider, {
                          visibility: event.target.value as
                            | ""
                            | "public"
                            | "private",
                        })
                      }
                    >
                      {destination.provider === "tiktok" && <option value="">Выберите видимость TikTok</option>}
                      <option value="public">Public — для всех</option>
                      <option value="private">Private — только аккаунт</option>
                    </select>
                  </label>
                </div>
                <p className="text-xs text-slate-400">
                  {destination.auto_publish
                    ? "Готовые клипы занимают ближайшие свободные места этого назначения."
                    : "Публикации готовятся, время назначается вручную в календаре."}{" "}
                  {destination.provider === "tiktok"
                    ? "TikTok получает только вертикальные клипы. Подборки продолжают выходить на YouTube."
                    : "Лимит этого YouTube-канала общий с другими его потоками."}
                </p>
              </>
            )}
          </section>
        );
      })}
      {!settings && (
        <Link href="/admin/accounts" className="text-sm text-indigo-400">
          Подключить и проверить аккаунты →
        </Link>
      )}
    </div>
  );
}
