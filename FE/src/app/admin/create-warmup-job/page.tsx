"use client";

import { useEffect, useState } from "react";
import { fetchAccountsList, createWarmupJob } from "@/lib/api";
import type { AccountRecord } from "@/types";

const STATUS_OPTIONS = ["pending", "warming_up", "done"];
const TYPE_OPTIONS = ["one-time", "cron"];
const REPEAT_UNITS = [
  { label: "sec",  factor: 1 },
  { label: "min",  factor: 60 },
  { label: "hour", factor: 3600 },
  { label: "day",  factor: 86400 },
];

export default function CreateWarmupJobPage() {
  const [accounts, setAccounts] = useState<AccountRecord[]>([]);
  const [accountId, setAccountId] = useState<number | "">("");
  const [status, setStatus] = useState("pending");
  const [type, setType] = useState("one-time");
  const [warmUpDatetime, setWarmUpDatetime] = useState("");
  const [repeatValue, setRepeatValue] = useState(1);
  const [repeatUnitIdx, setRepeatUnitIdx] = useState(0);
  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; message: string } | null>(null);

  useEffect(() => {
    fetchAccountsList().then(setAccounts).catch(() => {});
  }, []);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (accountId === "") return;
    setSubmitting(true);
    setResult(null);
    try {
      const repeatStep = type === "cron" ? repeatValue * REPEAT_UNITS[repeatUnitIdx].factor : null;
      const job = await createWarmupJob({
        account_id: accountId,
        status,
        type,
        warm_up_datetime: warmUpDatetime ? new Date(warmUpDatetime).toISOString() : null,
        repeat_step: repeatStep,
      });
      setResult({ ok: true, message: `Created warmup job ${job.job_id}` });
      // reset
      setAccountId("");
      setStatus("pending");
      setType("one-time");
      setWarmUpDatetime("");
      setRepeatValue(1);
      setRepeatUnitIdx(0);
    } catch (err) {
      setResult({ ok: false, message: String(err) });
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="p-6 max-w-lg flex flex-col gap-6">
      <h1 className="text-xl font-semibold text-slate-100">Create Warmup Job</h1>

      <form onSubmit={handleSubmit} className="bg-slate-800 border border-slate-700 rounded-xl p-5 flex flex-col gap-4">

        {/* Account */}
        <div className="flex flex-col gap-1">
          <label className="text-xs text-slate-400">Account</label>
          <select
            required
            value={accountId}
            onChange={(e) => setAccountId(Number(e.target.value))}
            className="bg-slate-700 border border-slate-600 text-slate-100 text-sm rounded-md px-3 py-2 focus:outline-none focus:ring-1 focus:ring-indigo-500"
          >
            <option value="" disabled>Select account…</option>
            {accounts.map((a) => (
              <option key={a.id} value={a.id}>[{a.platform}] {a.name}</option>
            ))}
          </select>
        </div>

        {/* Status */}
        <div className="flex flex-col gap-1">
          <label className="text-xs text-slate-400">Status</label>
          <select
            value={status}
            onChange={(e) => setStatus(e.target.value)}
            className="bg-slate-700 border border-slate-600 text-slate-100 text-sm rounded-md px-3 py-2 focus:outline-none focus:ring-1 focus:ring-indigo-500"
          >
            {STATUS_OPTIONS.map((s) => <option key={s} value={s}>{s}</option>)}
          </select>
        </div>

        {/* Type */}
        <div className="flex flex-col gap-1">
          <label className="text-xs text-slate-400">Type</label>
          <select
            value={type}
            onChange={(e) => setType(e.target.value)}
            className="bg-slate-700 border border-slate-600 text-slate-100 text-sm rounded-md px-3 py-2 focus:outline-none focus:ring-1 focus:ring-indigo-500"
          >
            {TYPE_OPTIONS.map((t) => <option key={t} value={t}>{t}</option>)}
          </select>
        </div>

        {/* Warm-up datetime */}
        <div className="flex flex-col gap-1">
          <label className="text-xs text-slate-400">Warm-up datetime</label>
          <input
            type="datetime-local"
            value={warmUpDatetime}
            onChange={(e) => setWarmUpDatetime(e.target.value)}
            className="bg-slate-700 border border-slate-600 text-slate-100 text-sm rounded-md px-3 py-2 focus:outline-none focus:ring-1 focus:ring-indigo-500"
          />
        </div>

        {/* Repeat step (cron only) */}
        {type === "cron" && (
          <div className="flex flex-col gap-1">
            <label className="text-xs text-slate-400">Repeat every</label>
            <div className="flex gap-2">
              <input
                type="number"
                min={1}
                value={repeatValue}
                onChange={(e) => setRepeatValue(Math.max(1, Number(e.target.value)))}
                className="w-28 bg-slate-700 border border-slate-600 text-slate-100 text-sm rounded-md px-3 py-2 focus:outline-none focus:ring-1 focus:ring-indigo-500"
              />
              <select
                value={repeatUnitIdx}
                onChange={(e) => setRepeatUnitIdx(Number(e.target.value))}
                className="bg-slate-700 border border-slate-600 text-slate-100 text-sm rounded-md px-3 py-2 focus:outline-none focus:ring-1 focus:ring-indigo-500"
              >
                {REPEAT_UNITS.map((u, i) => <option key={u.label} value={i}>{u.label}</option>)}
              </select>
            </div>
            <p className="text-xs text-slate-500 mt-0.5">
              = {repeatValue * REPEAT_UNITS[repeatUnitIdx].factor} seconds stored in DB
            </p>
          </div>
        )}

        <button
          type="submit"
          disabled={submitting || accountId === ""}
          className="mt-1 bg-indigo-600 hover:bg-indigo-500 disabled:opacity-40 text-white text-sm font-medium px-4 py-2 rounded-lg transition-colors"
        >
          {submitting ? "Creating…" : "Create"}
        </button>

        {result && (
          <p className={`text-sm ${result.ok ? "text-emerald-400" : "text-red-400"}`}>
            {result.message}
          </p>
        )}
      </form>
    </div>
  );
}
