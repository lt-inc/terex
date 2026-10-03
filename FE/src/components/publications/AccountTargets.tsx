"use client";
import { useEffect, useState } from "react";
import type { AccountRecord, PublicationTarget } from "@/types";
import {
  DEFAULT_TIMEZONE,
  TIMEZONES,
  zonedParts,
  zonePartsToISO,
} from "@/lib/datetime";
const input =
  "bg-slate-700 border border-slate-600 rounded-md px-2 py-1.5 text-sm text-slate-100 [color-scheme:dark]";
export default function AccountTargets({
  accounts,
  targets,
  onChange,
  requireTime = true,
}: {
  accounts: AccountRecord[];
  targets: PublicationTarget[];
  onChange: (targets: PublicationTarget[]) => void;
  requireTime?: boolean;
}) {
  function patch(id: number, changes: Partial<PublicationTarget>) {
    onChange(
      targets.map((t) => (t.account_id === id ? { ...t, ...changes } : t)),
    );
  }
  return (
    <div className="flex flex-col gap-3">
      <p className="text-xs font-semibold uppercase tracking-widest text-slate-400">
        Publication accounts
      </p>
      {accounts.length === 0 && (
        <p className="text-sm text-slate-500">
          Create and connect a YouTube account first.
        </p>
      )}
      {accounts.map((a) => {
        const target = targets.find((t) => t.account_id === a.id);
        return (
          <div
            key={a.id}
            className="border border-slate-700 rounded-lg p-3 flex flex-col gap-2"
          >
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                className="accent-indigo-500"
                checked={!!target}
                disabled={a.platform !== "youtube"}
                onChange={(e) =>
                  onChange(
                    e.target.checked
                      ? [
                          ...targets,
                          {
                            account_id: a.id,
                            start_at: null,
                            interval_seconds: 3600,
                            timezone: DEFAULT_TIMEZONE,
                          },
                        ]
                      : targets.filter((t) => t.account_id !== a.id),
                  )
                }
              />
              <span>
                {a.name} · {a.platform}
              </span>
              <span className="text-xs text-slate-500">
                {a.platform !== "youtube"
                  ? "Publishing coming later"
                  : (a.connection_state ?? "Not connected")}
              </span>
            </label>
            {target && (
              <TargetRow
                target={target}
                onChange={(p) => patch(a.id, p)}
                requireTime={requireTime}
              />
            )}
          </div>
        );
      })}
      {targets
        .filter((t) => !accounts.some((a) => a.id === t.account_id))
        .map((t) => (
          <div key={t.account_id} className="text-red-400 text-sm">
            Account {t.account_id} no longer exists.
            <button
              className="ml-3 underline"
              onClick={() =>
                onChange(targets.filter((x) => x.account_id !== t.account_id))
              }
            >
              Remove selection
            </button>
          </div>
        ))}
    </div>
  );
}
function TargetRow({
  target,
  onChange,
  requireTime,
}: {
  target: PublicationTarget;
  onChange: (p: Partial<PublicationTarget>) => void;
  requireTime: boolean;
}) {
  const [value, setValue] = useState(() => {
      const p = target.start_at
        ? zonedParts(target.start_at, target.timezone)
        : null;
      return p ? `${p.date}T${p.time}` : "";
    }),
    [error, setError] = useState(""),
    [unit, setUnit] = useState(() =>
      target.interval_seconds > 0 && target.interval_seconds % 86400 === 0
        ? 86400
        : target.interval_seconds > 0 && target.interval_seconds % 3600 === 0
          ? 3600
          : 60,
    );
  useEffect(() => {
    if (target.start_at) {
      const p = zonedParts(target.start_at, target.timezone);
      setValue(`${p.date}T${p.time}`);
      setError("");
    }
  }, [target.start_at, target.timezone]);
  function setTime(next: string) {
    setValue(next);
    setError("");
    if (!next) {
      onChange({ start_at: null });
      return;
    }
    try {
      const [d, t] = next.split("T");
      onChange({ start_at: zonePartsToISO(d, t, target.timezone) });
    } catch (e) {
      onChange({ start_at: null });
      setError(e instanceof Error ? e.message : String(e));
    }
  }
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1 text-xs text-slate-400">
          Start
          <input
            type="datetime-local"
            required={requireTime}
            className={input}
            value={value}
            onChange={(e) => setTime(e.target.value)}
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-slate-400">
          Timezone
          <select
            className={input}
            value={target.timezone}
            onChange={(e) => onChange({ timezone: e.target.value })}
          >
            {Array.from(new Set([...TIMEZONES, target.timezone])).map((z) => (
              <option key={z}>{z}</option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-slate-400">
          Interval
          <input
            type="number"
            min={0}
            step="any"
            className={`${input} w-24`}
            value={target.interval_seconds / unit}
            onChange={(e) =>
              onChange({ interval_seconds: Number(e.target.value) * unit })
            }
          />
        </label>
        <select
          aria-label="Interval unit"
          className={input}
          value={unit}
          onChange={(e) => setUnit(Number(e.target.value))}
        >
          <option value={60}>Minutes</option>
          <option value={3600}>Hours</option>
          <option value={86400}>Days</option>
        </select>
      </div>
      {error && (
        <p role="alert" className="text-xs text-red-400">
          {error}
        </p>
      )}
      {!requireTime && (
        <p className="text-xs text-slate-500">
          Leave start blank to schedule from approval.
        </p>
      )}
    </div>
  );
}
