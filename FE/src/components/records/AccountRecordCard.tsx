"use client";

import { useState, useCallback } from "react";
import Link from "next/link";
import {
  fetchAccountScheduled,
  fetchAccountPublished,
  toUTC7,
  connectAccount,
  checkAccount,
  disconnectAccount,
  confirmAccount,
} from "@/lib/api";
import type { AccountRecord, OutData } from "@/types";
import TikTokConnectionPanel from "./TikTokConnectionPanel";

interface AccountRecordCardProps {
  record: AccountRecord;
  onUpdated: (r: AccountRecord) => void;
  onDeleted: () => void;
  onSave: (id: number, data: Partial<AccountRecord>) => Promise<AccountRecord>;
  onDelete: (id: number) => Promise<void>;
}

const PLATFORMS = [
  { value: "youtube", label: "YouTube" },
  { value: "tiktok", label: "TikTok" },
  { value: "instagram", label: "Instagram" },
];
const PLATFORM_COLORS: Record<string, string> = {
  youtube: "bg-red-900/40 text-red-300",
  tiktok: "bg-slate-700 text-slate-300",
  instagram: "bg-purple-900/40 text-purple-300",
};

const STATUS_COLORS: Record<string, string> = {
  done: "text-emerald-400",
  published: "text-emerald-400",
  pending: "text-yellow-400",
  processing: "text-blue-400",
  failed: "text-red-400",
};

function ChevronIcon({ open }: { open: boolean }) {
  return (
    <svg
      className={`w-4 h-4 transition-transform duration-200 shrink-0 ${open ? "rotate-90" : ""}`}
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth={2}
        d="M9 5l7 7-7 7"
      />
    </svg>
  );
}

function groupByJob(clips: OutData[]): [string, OutData[]][] {
  const map = new Map<string, OutData[]>();
  for (const clip of clips) {
    const key = clip.job_id ?? "unknown";
    if (!map.has(key)) map.set(key, []);
    map.get(key)!.push(clip);
  }
  return Array.from(map.entries());
}

function JobGroup({
  jobId,
  clips,
  dateField,
}: {
  jobId: string;
  clips: OutData[];
  dateField: "publish_date" | "published_at";
}) {
  const [open, setOpen] = useState(false);
  return (
    <div className="border border-slate-700 rounded-lg overflow-hidden mb-2 last:mb-0">
      <button
        onClick={() => setOpen((o) => !o)}
        className="w-full flex items-center gap-2 px-3 py-2 bg-slate-750 hover:bg-slate-700 transition-colors text-left"
      >
        <ChevronIcon open={open} />
        <Link
          href={`/admin/jobs#${jobId}`}
          onClick={(e) => e.stopPropagation()}
          className="text-sm font-mono text-indigo-400 hover:text-indigo-300 hover:underline transition-colors"
        >
          {jobId}
        </Link>
        <span className="text-xs text-slate-500 ml-auto">
          {clips.length} clips
        </span>
      </button>
      {open && (
        <div className="px-3 pb-1">
          {clips.map((clip) => (
            <ClipRow key={clip.id} clip={clip} dateField={dateField} />
          ))}
        </div>
      )}
    </div>
  );
}

function ClipRow({
  clip,
  dateField,
}: {
  clip: OutData;
  dateField: "publish_date" | "published_at";
}) {
  const raw = clip[dateField];
  const dateStr = raw ? toUTC7(raw) : "—";
  const statusColor = STATUS_COLORS[clip.status ?? ""] ?? "text-slate-400";
  return (
    <div className="grid grid-cols-[1fr_auto_auto] items-center gap-x-6 py-2 border-b border-slate-700/50 last:border-0">
      <div className="flex flex-col gap-0.5 min-w-0">
        <span className="text-sm text-slate-200 truncate">
          {clip.name ?? clip.video_name ?? "—"}
        </span>
        <div className="flex items-center gap-3 text-xs flex-wrap">
          <span className="text-slate-500">DB ID: {clip.id}</span>
          {clip.job_id && (
            <Link
              href={`/admin/jobs#${clip.job_id}`}
              className="text-indigo-400 hover:text-indigo-300 hover:underline transition-colors"
            >
              job: {clip.job_id}
            </Link>
          )}
          {clip.sub_job_id && (
            <Link
              href={`/admin/jobs#${clip.sub_job_id}`}
              className="text-indigo-400 hover:text-indigo-300 hover:underline transition-colors"
            >
              sub job: {clip.sub_job_id}
            </Link>
          )}
        </div>
      </div>
      <div className="flex flex-col items-end gap-0.5">
        <span className="text-sm text-slate-400 whitespace-nowrap">
          {dateStr} <span className="text-xs text-slate-600">UTC+7</span>
        </span>
        {dateField === "publish_date" && clip.published_at && (
          <span className="text-xs text-emerald-400 whitespace-nowrap">
            published: {toUTC7(clip.published_at)}{" "}
            <span className="text-emerald-700">UTC+7</span>
          </span>
        )}
        {dateField === "published_at" && clip.publish_date && (
          <span className="text-xs text-slate-500 whitespace-nowrap">
            scheduled: {toUTC7(clip.publish_date)}{" "}
            <span className="text-slate-700">UTC+7</span>
          </span>
        )}
      </div>
      <span className={`text-sm font-medium whitespace-nowrap ${statusColor}`}>
        {clip.status ?? "—"}
      </span>
    </div>
  );
}

export default function AccountRecordCard({
  record,
  onUpdated,
  onDeleted,
  onSave,
  onDelete,
}: AccountRecordCardProps) {
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [platform, setPlatform] = useState(record.platform);
  const [name, setName] = useState(record.name);
  const [url, setUrl] = useState(record.url ?? "");
  const [note, setNote] = useState(record.note ?? "");
  const [connectionBusy, setConnectionBusy] = useState(false);
  const [actionError, setActionError] = useState("");

  async function connection(
    action: "connect" | "check" | "disconnect" | "confirm",
  ) {
    if (
      action === "disconnect" &&
      !confirm(
        `Disconnect ${record.name}? Future publications will require reconnecting.`,
      )
    )
      return;
    setConnectionBusy(true);
    setActionError("");
    try {
      if (action === "connect") {
        const result = await connectAccount(record);
        const url = new URL(result.url);
        if (url.protocol !== "https:" || url.hostname !== "accounts.google.com")
          throw new Error("Unexpected Google connection URL");
        window.location.assign(result.url);
      } else {
        onUpdated(
          await (action === "check"
            ? checkAccount(record.id)
            : action === "confirm"
              ? confirmAccount(record)
              : disconnectAccount(record)),
        );
      }
    } catch (e) {
      setActionError(e instanceof Error ? e.message : String(e));
    } finally {
      setConnectionBusy(false);
    }
  }

  // Expand panel
  const [expanded, setExpanded] = useState(false);
  const [scheduled, setScheduled] = useState<OutData[] | null>(null);
  const [scheduledLoading, setScheduledLoading] = useState(false);
  const [scheduledError, setScheduledError] = useState<string | null>(null);

  const [published, setPublished] = useState<OutData[] | null>(null);
  const [publishedLoading, setPublishedLoading] = useState(false);
  const [publishedError, setPublishedError] = useState<string | null>(null);
  const [showPublished, setShowPublished] = useState(false);

  const loadScheduled = useCallback(async () => {
    setScheduledLoading(true);
    setScheduledError(null);
    try {
      setScheduled(await fetchAccountScheduled(record.id));
    } catch (e) {
      setScheduledError(String(e));
    } finally {
      setScheduledLoading(false);
    }
  }, [record.id]);

  const loadPublished = useCallback(async () => {
    setPublishedLoading(true);
    setPublishedError(null);
    try {
      setPublished(await fetchAccountPublished(record.id));
    } catch (e) {
      setPublishedError(String(e));
    } finally {
      setPublishedLoading(false);
    }
  }, [record.id]);

  function handleToggleExpand() {
    const next = !expanded;
    setExpanded(next);
    if (next && scheduled === null) {
      loadScheduled();
    }
  }

  function handleShowPublished() {
    if (!showPublished) {
      setShowPublished(true);
      if (published === null) loadPublished();
    } else {
      setShowPublished(false);
    }
  }

  function reset() {
    setPlatform(record.platform);
    setName(record.name);
    setUrl(record.url ?? "");
    setNote(record.note ?? "");
    setEditing(false);
  }

  async function handleSave() {
    setSaving(true);
    try {
      const updated = await onSave(record.id, { platform, name, url, note });
      onUpdated(updated);
      setEditing(false);
    } catch (e) {
      setActionError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!confirm(`Delete account "${record.name}"?`)) return;
    try {
      await onDelete(record.id);
      onDeleted();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div className="bg-slate-800 border border-slate-700 rounded-xl flex flex-col">
      {/* Card header */}
      <div className="p-4 flex flex-col gap-3">
        {!editing ? (
          <div className="flex items-start justify-between gap-2">
            <div className="flex flex-col gap-1.5 flex-1 min-w-0">
              <div className="flex items-center gap-2">
                <span
                  className={`text-xs font-medium px-2 py-0.5 rounded shrink-0 ${PLATFORM_COLORS[record.platform] ?? "bg-slate-700 text-slate-300"}`}
                >
                  {record.platform}
                </span>
                <span className="text-sm font-medium text-slate-100 truncate">
                  {record.name}
                </span>
              </div>
              {record.url && (
                <a
                  href={record.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-xs text-indigo-400 hover:underline truncate max-w-sm"
                >
                  {record.url}
                </a>
              )}
              {record.note && (
                <span className="text-xs text-slate-400">{record.note}</span>
              )}
              <span
                className={`text-xs ${record.connection_state === "connected" ? "text-emerald-400" : "text-amber-400"}`}
              >
                Connection: {record.connection_state ?? "disconnected"}
              </span>
              {record.remote_account_id && record.platform === "youtube" && (
                <span className="text-xs text-slate-500">
                  Channel:{" "}
                  {record.remote_account_name ?? record.remote_account_id}
                  <a
                    className="text-indigo-400 ml-2"
                    href={`https://www.youtube.com/channel/${encodeURIComponent(record.remote_account_id)}`}
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    Verify on YouTube →
                  </a>
                </span>
              )}
              {record.platform === "youtube" && record.connection_state === "pending_confirmation" && (
                <div className="border border-amber-700 rounded-lg p-3 text-xs text-amber-300">
                  <p>
                    Verify that this is the YouTube channel you intend to
                    publish to:{" "}
                    {record.remote_account_name ?? record.remote_account_id} (
                    {record.remote_account_id}).
                  </p>
                  <button
                    disabled={connectionBusy}
                    className="mt-2 bg-indigo-600 rounded-md px-3 py-1.5 text-white disabled:opacity-40"
                    onClick={() => {
                      if (
                        confirm(
                          `Confirm YouTube destination ${record.remote_account_id}?`,
                        )
                      )
                        connection("confirm");
                    }}
                  >
                    Confirm this channel
                  </button>
                </div>
              )}
              {record.connection_error && (
                <span className="text-xs text-red-400">
                  {record.connection_error}
                </span>
              )}
              {record.last_checked_at && (
                <span className="text-xs text-slate-500">
                  Checked: {toUTC7(record.last_checked_at)} UTC+7
                </span>
              )}
              {record.platform === "youtube" ? (
                <div className="flex gap-3 text-xs mt-1">
                  <button
                    disabled={connectionBusy}
                    className="text-indigo-400 disabled:opacity-40"
                    onClick={() => connection("connect")}
                  >
                    {record.connection_state === "connected"
                      ? "Reconnect with Google"
                      : "Connect with Google"}
                  </button>
                  <button
                    disabled={connectionBusy}
                    className="text-slate-400 disabled:opacity-40"
                    onClick={() => connection("check")}
                  >
                    Check connection
                  </button>
                  {record.connection_state === "connected" && (
                    <button
                      disabled={connectionBusy}
                      className="text-red-400 disabled:opacity-40"
                      onClick={() => connection("disconnect")}
                    >
                      Disconnect
                    </button>
                  )}
                </div>
              ) : record.platform === "tiktok" ? (
                <TikTokConnectionPanel account={record} onUpdated={onUpdated} />
              ) : (
                <span className="text-xs text-slate-500">
                  Provider connection will be available later.
                </span>
              )}
            </div>

            <div className="flex items-center gap-2 shrink-0">
              <button
                onClick={() => setEditing(true)}
                className="text-xs text-indigo-400 hover:text-indigo-300 transition-colors"
              >
                Edit
              </button>
              <button
                onClick={handleDelete}
                className="text-xs text-red-500 hover:text-red-400 transition-colors"
              >
                Delete
              </button>
              <button
                onClick={handleToggleExpand}
                className="text-slate-400 hover:text-slate-200 transition-colors"
                title="Show schedule"
              >
                <ChevronIcon open={expanded} />
              </button>
            </div>
          </div>
        ) : (
          <div className="flex flex-col gap-2">
            <div className="grid grid-cols-2 gap-2">
              <div className="flex flex-col gap-1">
                <label className="text-xs text-slate-400">Platform</label>
                <select
                  value={platform}
                  onChange={(e) => setPlatform(e.target.value)}
                  className="bg-slate-700 border border-slate-600 rounded-md px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500"
                >
                  {PLATFORMS.map((p) => (
                    <option key={p.value} value={p.value}>
                      {p.label}
                    </option>
                  ))}
                </select>
              </div>
              <div className="flex flex-col gap-1">
                <label className="text-xs text-slate-400">Name</label>
                <input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  className="bg-slate-700 border border-slate-600 rounded-md px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500"
                />
              </div>
            </div>
            <div className="flex flex-col gap-1">
              <label className="text-xs text-slate-400">URL</label>
              <input
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                className="bg-slate-700 border border-slate-600 rounded-md px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500"
              />
            </div>
            <div className="flex flex-col gap-1">
              <label className="text-xs text-slate-400">Note</label>
              <input
                value={note}
                onChange={(e) => setNote(e.target.value)}
                className="bg-slate-700 border border-slate-600 rounded-md px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500"
              />
            </div>
            <div className="flex gap-2">
              <button
                onClick={handleSave}
                disabled={saving}
                className="bg-indigo-600 hover:bg-indigo-500 disabled:opacity-40 text-white text-xs font-medium px-3 py-1.5 rounded-md transition-colors"
              >
                {saving ? "Saving…" : "Save"}
              </button>
              <button
                onClick={reset}
                className="text-xs text-slate-400 hover:text-slate-200 transition-colors px-2"
              >
                Cancel
              </button>
            </div>
          </div>
        )}
      </div>

      {/* Expanded panel */}
      {actionError && (
        <p role="alert" className="text-sm text-red-400 px-4 pb-3">
          {actionError}
        </p>
      )}
      {expanded && (
        <div className="border-t border-slate-700 px-4 py-3 flex flex-col gap-3">
          {/* Scheduled clips */}
          <div>
            <div className="flex items-center justify-between mb-3">
              <span className="text-sm font-semibold text-slate-300 uppercase tracking-wide">
                Upload Schedule
              </span>
              <button
                onClick={loadScheduled}
                className="text-sm text-slate-500 hover:text-slate-300 transition-colors"
              >
                ↻ refresh
              </button>
            </div>
            {scheduledLoading ? (
              <span className="text-sm text-slate-500">Loading…</span>
            ) : scheduledError ? (
              <span className="text-sm text-red-400">{scheduledError}</span>
            ) : scheduled && scheduled.length > 0 ? (
              <div className="flex flex-col">
                {groupByJob(scheduled).map(([jobId, clips]) => (
                  <JobGroup
                    key={jobId}
                    jobId={jobId}
                    clips={clips}
                    dateField="publish_date"
                  />
                ))}
              </div>
            ) : (
              <span className="text-sm text-slate-500">No scheduled clips</span>
            )}
          </div>

          {/* Published clips toggle */}
          <div>
            <button
              onClick={handleShowPublished}
              className="flex items-center gap-1.5 text-sm font-medium text-slate-400 hover:text-slate-200 transition-colors"
            >
              <ChevronIcon open={showPublished} />
              Published clips
              {published !== null ? ` (${published.length})` : ""}
            </button>

            {showPublished && (
              <div className="mt-3">
                {publishedLoading ? (
                  <span className="text-sm text-slate-500">Loading…</span>
                ) : publishedError ? (
                  <span className="text-sm text-red-400">{publishedError}</span>
                ) : published && published.length > 0 ? (
                  <div className="max-h-96 overflow-y-auto min-h-0">
                    {groupByJob(published).map(([jobId, clips]) => (
                      <JobGroup
                        key={jobId}
                        jobId={jobId}
                        clips={clips}
                        dateField="published_at"
                      />
                    ))}
                  </div>
                ) : (
                  <span className="text-sm text-slate-500">
                    No published clips
                  </span>
                )}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
