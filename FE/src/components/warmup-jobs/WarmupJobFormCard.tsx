"use client";

import type { AccountRecord } from "@/types";

const TYPE_OPTIONS = ["one-time", "cron"];
const REPEAT_UNITS = [
  { label: "sec",  factor: 1 },
  { label: "min",  factor: 60 },
  { label: "hour", factor: 3600 },
  { label: "day",  factor: 86400 },
];

export interface WarmupJobFormData {
  id: string;
  accountId: number | "";
  type: string;
  warmUpDatetime: string;
  repeatValue: number;
  repeatUnitIdx: number;
}

interface Props {
  card: WarmupJobFormData;
  accounts: AccountRecord[];
  onChange: (card: WarmupJobFormData) => void;
  onDelete: () => void;
}

export function createEmptyWarmupCard(): WarmupJobFormData {
  return {
    id: crypto.randomUUID(),
    accountId: "",
    type: "one-time",
    warmUpDatetime: "",
    repeatValue: 1,
    repeatUnitIdx: 0,
  };
}

export default function WarmupJobFormCard({ card, accounts, onChange, onDelete }: Props) {
  function update<K extends keyof WarmupJobFormData>(key: K, value: WarmupJobFormData[K]) {
    onChange({ ...card, [key]: value });
  }

  const repeatSeconds = card.repeatValue * REPEAT_UNITS[card.repeatUnitIdx].factor;

  return (
    <div className="relative bg-slate-800 border border-slate-700 rounded-xl p-5 flex flex-col gap-4">
      <button
        onClick={onDelete}
        className="absolute top-3 right-3 w-7 h-7 flex items-center justify-center rounded-full text-slate-400 hover:text-white hover:bg-slate-700 transition-colors text-lg leading-none"
      >
        &times;
      </button>

      {/* Account */}
      <div className="flex flex-col gap-1 pr-8">
        <label className="text-xs text-slate-400">Account</label>
        <select
          value={card.accountId}
          onChange={(e) => update("accountId", e.target.value === "" ? "" : Number(e.target.value))}
          className="bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500"
        >
          <option value="">— select account —</option>
          {accounts.map((a) => (
            <option key={a.id} value={a.id}>[{a.platform}] {a.name}</option>
          ))}
        </select>
      </div>

      {/* Type */}
      <div className="flex flex-col gap-1">
        <label className="text-xs text-slate-400">Type</label>
        <div className="flex gap-2">
          {TYPE_OPTIONS.map((t) => (
            <button
              key={t}
              type="button"
              onClick={() => update("type", t)}
              className={`px-4 py-1.5 text-sm rounded-md border transition-colors ${
                card.type === t
                  ? "bg-indigo-600 border-indigo-500 text-white"
                  : "bg-slate-700 border-slate-600 text-slate-300 hover:bg-slate-600"
              }`}
            >
              {t}
            </button>
          ))}
        </div>
      </div>

      {/* Warm-up datetime */}
      <div className="flex flex-col gap-1">
        <label className="text-xs text-slate-400">Warm-up datetime</label>
        <input
          type="datetime-local"
          value={card.warmUpDatetime}
          onChange={(e) => update("warmUpDatetime", e.target.value)}
          className="bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500"
        />
      </div>

      {/* Repeat step (cron only) */}
      {card.type === "cron" && (
        <div className="flex flex-col gap-1">
          <label className="text-xs text-slate-400">Repeat every</label>
          <div className="flex gap-2 items-center">
            <input
              type="number"
              min={1}
              value={card.repeatValue}
              onChange={(e) => update("repeatValue", Math.max(1, Number(e.target.value)))}
              className="w-24 bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500"
            />
            <div className="flex gap-1">
              {REPEAT_UNITS.map((u, i) => (
                <button
                  key={u.label}
                  type="button"
                  onClick={() => update("repeatUnitIdx", i)}
                  className={`px-3 py-1.5 text-xs rounded-md border transition-colors ${
                    card.repeatUnitIdx === i
                      ? "bg-indigo-600 border-indigo-500 text-white"
                      : "bg-slate-700 border-slate-600 text-slate-300 hover:bg-slate-600"
                  }`}
                >
                  {u.label}
                </button>
              ))}
            </div>
            <span className="text-xs text-slate-500">= {repeatSeconds}s</span>
          </div>
        </div>
      )}
    </div>
  );
}
