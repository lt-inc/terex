"use client";
import { useState } from "react";
import Link from "next/link";
import AccountTargets from "@/components/publications/AccountTargets";
import {
  ApiError,
  deleteSubJob,
  fetchAccountsList,
  fetchSubJobTargets,
  saveSubJobTargets,
  toUTC7,
} from "@/lib/api";
import { validateTargets } from "@/lib/validation";
import type {
  AccountRecord,
  AccountsResponse,
  PublicationTarget,
  SubJobData,
} from "@/types";
interface Props {
  subJob: SubJobData;
  accounts: AccountsResponse;
  bannerOptions: string[];
  locked?: boolean;
  defaultOpen?: boolean;
  onUpdated: (s: SubJobData) => void;
  onDeleted: () => void;
}
export default function SubJobCard({
  subJob,
  locked = false,
  defaultOpen = false,
  onUpdated,
  onDeleted,
}: Props) {
  const [open, setOpen] = useState(defaultOpen),
    [editing, setEditing] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [targets, setTargets] = useState<PublicationTarget[]>([]),
    [records, setRecords] = useState<AccountRecord[]>([]),
    [version, setVersion] = useState(subJob.version ?? 1);
  async function edit() {
    setBusy(true);
    setError("");
    try {
      const [data, accounts] = await Promise.all([
        fetchSubJobTargets(subJob.sub_job_id),
        fetchAccountsList(),
      ]);
      setTargets(data.targets);
      setVersion(data.version);
      setRecords(accounts);
      setEditing(true);
      setOpen(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  async function save() {
    const invalid = validateTargets(targets, false);
    if (invalid) {
      setError(invalid);
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await saveSubJobTargets(
        subJob.sub_job_id,
        version,
        targets,
      );
      setTargets(result.targets);
      setVersion(result.version);
      onUpdated({
        ...subJob,
        version: result.version,
        account_ids: targets.map((t) => t.account_id),
        account_names: targets.map(
          (t) =>
            records.find((a) => a.id === t.account_id)?.name ??
            String(t.account_id),
        ),
      });
      setEditing(false);
    } catch (e) {
      setError(
        e instanceof ApiError && e.status === 409
          ? "Targets changed or an upload started. Reload targets before retrying."
          : e instanceof Error
            ? e.message
            : String(e),
      );
    } finally {
      setBusy(false);
    }
  }
  async function remove() {
    if (!confirm(`Delete sub-job ${subJob.sub_job_id}?`)) return;
    setBusy(true);
    try {
      await deleteSubJob(subJob.sub_job_id);
      onDeleted();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div
      id={subJob.sub_job_id}
      className="bg-slate-900 border border-slate-700 rounded-lg overflow-hidden"
    >
      <div className="px-4 py-3 flex gap-3 items-center">
        <button
          className="flex-1 text-left text-xs text-slate-400"
          onClick={() => setOpen((v) => !v)}
        >
          {open ? "▾" : "▸"} {subJob.sub_job_id} · {subJob.status} ·{" "}
          {subJob.outs?.length ?? 0} publications
        </button>
        <button
          disabled={busy}
          className="text-indigo-400 text-xs disabled:opacity-40"
          onClick={edit}
        >
          Account schedules
        </button>
        {!locked && (
          <button
            disabled={busy}
            aria-label="Delete sub-job"
            className="text-red-400"
            onClick={remove}
          >
            ×
          </button>
        )}
      </div>
      {error && (
        <p role="alert" className="text-red-400 text-sm px-4 pb-3">
          {error}
        </p>
      )}
      {open && (
        <div className="border-t border-slate-700 px-4 py-3 flex flex-col gap-3">
          <p className="text-sm text-slate-400">
            Accounts: {subJob.account_names.join(", ") || "None"} · Banner:{" "}
            {subJob.banner_name ?? "None"}
          </p>
          {editing ? (
            <>
              <p className="text-xs text-amber-300">
                Each destination has its own schedule. Changes apply to future
                uploads; already started publications keep their settings. Older
                backlog still requires manual confirmation in Calendar.
              </p>
              <AccountTargets
                accounts={records}
                targets={targets}
                onChange={setTargets}
                requireTime={false}
              />
              <div className="flex gap-3">
                <button
                  disabled={busy}
                  onClick={save}
                  className="bg-indigo-600 disabled:opacity-40 px-3 py-2 rounded-lg text-sm"
                >
                  Save account schedules
                </button>
                <button
                  disabled={busy}
                  onClick={edit}
                  className="text-slate-400 text-sm"
                >
                  Reload
                </button>
                <button
                  disabled={busy}
                  onClick={() => setEditing(false)}
                  className="text-slate-400 text-sm"
                >
                  Cancel
                </button>
              </div>
            </>
          ) : (
            <>
              {subJob.fail_place && (
                <p className="text-red-400 text-sm">
                  Failed: {subJob.fail_place}
                </p>
              )}
              {subJob.outs?.map((out) => (
                <div
                  key={out.id}
                  className="flex flex-wrap gap-3 text-xs text-slate-400 border-t border-slate-800 pt-2"
                >
                  <span>{out.status}</span>
                  <span>
                    {out.account_name} · {out.platform}
                  </span>
                  <span className="flex-1">{out.name ?? out.video_name}</span>
                  {out.publish_date && (
                    <span>{toUTC7(out.publish_date)} UTC+7</span>
                  )}
                </div>
              ))}
              <Link href="/admin/calendar" className="text-indigo-400 text-sm">
                Manage individual publications in Calendar →
              </Link>
            </>
          )}
        </div>
      )}
    </div>
  );
}
