"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import Toggle from "@/components/ui/Toggle";
import ConfirmAction from "@/components/ui/ConfirmAction";
import {
  archiveTwitchChannel, checkTwitchNow, createTwitchChannel, fetchAccountsList,
  fetchImports, fetchTwitchChannels, fetchTwitchClips, fetchTwitchSettings,
  fetchTwitchStatus, rejectImport, saveTwitchSettings, toUTC7, updateTwitchChannel,
} from "@/lib/api";
import { DEFAULT_TWITCH_SETTINGS, validateTwitchSettings } from "@/lib/twitch";
import type { AccountRecord } from "@/types";
import type { ImportedClip, TwitchChannel, TwitchClip, TwitchCollectorStatus, TwitchSettings } from "@/types/twitch";

const input = "w-full min-w-0 rounded-md border border-slate-600 bg-slate-700 px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500";
const button = "rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600 disabled:opacity-40";
const panel = "rounded-xl border border-slate-700 bg-slate-800 p-4 sm:p-5";
type NumericField = Exclude<keyof TwitchSettings, "enabled" | "timezone">;
const discoveryFields: [NumericField, string, number, number, number?][] = [
  ["poll_interval_minutes", "Check every (minutes)", 1, 10080],
  ["window_hours", "Recent clips window (hours)", 1, 720],
  ["first", "Clips fetched per channel", 1, 100],
  ["min_views", "Minimum views", 0, Number.MAX_SAFE_INTEGER],
  ["min_age_minutes", "Minimum clip age (minutes)", 0, Number.MAX_SAFE_INTEGER],
  ["min_duration_sec", "Minimum duration (seconds)", 0.1, 60, 0.1],
  ["max_duration_sec", "Maximum duration (seconds)", 0.1, 60, 0.1],
];
const outputFields: [NumericField, string, number, number][] = [
  ["daily_downloads", "Total downloads per day", 1, 100],
  ["per_channel_daily_downloads", "Downloads per channel per day", 1, 100],
  ["daily_shorts", "Shorts per day", 1, 100],
  ["daily_compilations", "Compilations per day", 1, 10],
  ["compilation_min_clips", "Minimum clips in a compilation", 3, 10],
  ["compilation_max_clips", "Maximum clips in a compilation", 3, 10],
  ["compilation_hour", "Compilation hour (0–23)", 0, 23],
  ["backlog_limit", "Maximum imports awaiting review", 1, 1000],
];
const messageOf = (error: unknown) => error instanceof Error ? error.message : String(error);
const checkedAt = (value?: string | null) => value ? `${toUTC7(value)} · Asia/Tomsk` : "Not checked yet";

export default function TwitchPage() {
  const [settings, setSettings] = useState<TwitchSettings>({ ...DEFAULT_TWITCH_SETTINGS });
  const [settingsLoaded, setSettingsLoaded] = useState(false);
  const [settingsError, setSettingsError] = useState("");
  const [accounts, setAccounts] = useState<AccountRecord[]>([]);
  const [channels, setChannels] = useState<TwitchChannel[]>([]);
  const [clips, setClips] = useState<TwitchClip[]>([]);
  const [imports, setImports] = useState<ImportedClip[]>([]);
  const [status, setStatus] = useState<TwitchCollectorStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [channelInput, setChannelInput] = useState("");
  const [showArchived, setShowArchived] = useState(false);
  const [confirmation, setConfirmation] = useState<{ kind: "archive"; channel: TwitchChannel } | { kind: "reject"; item: ImportedClip } | null>(null);
  const connectedAccounts = accounts.filter((account) => account.platform === "youtube" && account.connection_state === "connected");

  const refresh = useCallback(async (showLoading = true) => {
    if (showLoading) setLoading(true);
    const results = await Promise.allSettled([fetchTwitchChannels(), fetchTwitchClips(), fetchTwitchStatus(), fetchImports()]);
    const [channelResult, clipResult, statusResult, importResult] = results;
    if (channelResult.status === "fulfilled") setChannels(channelResult.value ?? []);
    if (clipResult.status === "fulfilled") setClips(clipResult.value ?? []);
    if (statusResult.status === "fulfilled") setStatus(statusResult.value);
    else setStatus(null);
    if (importResult.status === "fulfilled") setImports(importResult.value ?? []);
    const failures = results.filter((result) => result.status === "rejected");
    if (showLoading || failures.length) setError(failures.map((result) => messageOf(result.reason)).join("; "));
    if (showLoading) setLoading(false);
  }, []);

  const loadSettings = useCallback(async () => {
    setSettingsError("");
    try {
      const [loadedSettings, loadedAccounts] = await Promise.all([fetchTwitchSettings(), fetchAccountsList()]);
      setSettings(loadedSettings); setSettingsLoaded(true); setAccounts(loadedAccounts ?? []);
    } catch (failure) { setSettingsError(messageOf(failure)); }
  }, []);
  useEffect(() => {
    void loadSettings();
    void refresh();
  }, [refresh, loadSettings]);

  useEffect(() => {
    // Scheduled checks can start and finish while the page is idle. Keep their
    // results current without flashing the loading state on every poll.
    const timer = setInterval(() => {
      if (document.visibilityState === "visible") void refresh(false);
    }, 10000);
    return () => clearInterval(timer);
  }, [refresh]);

  async function action(key: string, run: () => Promise<void>) {
    setBusy(key); setError(""); setNotice("");
    try { await run(); }
    catch (failure) { setError(messageOf(failure)); }
    finally { setBusy(""); }
  }

  async function save() {
    const issue = validateTwitchSettings(settings);
    if (issue) { setError(issue); return; }
    if (settings.enabled && !connectedAccounts.some((account) => account.id === settings.target_account_id)) {
      setError("Connect and confirm the selected YouTube account before enabling collection."); return;
    }
    await action("settings", async () => {
      const saved = await saveTwitchSettings(settings);
      setSettings(saved);
      setNotice(saved.enabled ? "Settings saved. Scheduled collection is enabled." : "Settings saved. Collection remains off.");
      await refresh();
    });
  }

  function fields(definitions: [NumericField, string, number, number, number?][]) {
    return definitions.map(([name, label, minimum, maximum, step]) => (
      <label key={name} className="flex flex-col gap-1 text-xs text-slate-400">
        {label}
        <input className={input} type="number" value={Number.isFinite(settings[name]) ? settings[name] : ""}
          min={minimum} max={maximum} step={step ?? 1} required
          onChange={(event) => setSettings((previous) => ({ ...previous, [name]: event.target.value === "" ? NaN : Number(event.target.value) }))} />
      </label>
    ));
  }

  return (
    <div className="flex min-h-full flex-col gap-5 p-4 sm:p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">Twitch sources</h1>
        <div className="flex gap-2">
          <button className={button} disabled={loading || !!busy} onClick={() => void refresh()}>{loading ? "Refreshing…" : "Refresh"}</button>
          <button className="rounded-lg bg-indigo-600 px-3 py-2 text-sm hover:bg-indigo-500 disabled:opacity-40"
            disabled={!!busy || !status?.credentials_ready || status.running || status.paused || !!status.block_reason}
            onClick={() => void action("check", async () => { await checkTwitchNow(); setNotice("Check requested. New clips still require review before publication."); await refresh(); })}>
            {status?.running ? "Checking…" : "Check now"}
          </button>
        </div>
      </div>
      <p className="text-sm text-slate-400">Collect recent popular clips, then review them in Terex. Adding a source does not verify content rights.</p>
      {error && <p role="alert" className="text-sm text-red-400">{error}</p>}
      {notice && <p role="status" className="text-sm text-emerald-400">{notice}</p>}
      <section className={panel} aria-labelledby="collector-title">
        <h2 id="collector-title" className="mb-3 font-semibold">Collector</h2>
        {status ? <>
          <div className="flex flex-wrap gap-3 text-sm">
            <span className={status.credentials_ready ? "text-emerald-400" : "text-amber-300"}>{status.credentials_ready ? "Twitch credentials ready" : "Twitch credentials not configured"}</span>
            <span>{status.running ? "Checking channels" : status.paused ? "Paused" : "Ready"}</span>
            <span className="text-slate-400">{status.channels} channels · {status.queued} queued · {status.downloading} downloading · {status.imported} imported · {status.failed} failed</span>
          </div>
          <p className="mt-2 text-xs text-slate-400">Last successful check: {checkedAt(status.last_check_at)}</p>
          {(status.block_reason || status.paused) && <p role="status" className="mt-2 text-sm text-amber-300">
            {status.block_reason || "Collection is off. Choose a connected YouTube account, enable scheduled collection and save settings."}
          </p>}
          {status.last_error && status.last_error !== status.block_reason && <p role="alert" className="mt-2 text-sm text-red-400">Last check message: {status.last_error}</p>}
        </> : <p className="text-sm text-slate-400">{loading ? "Loading collector status…" : "Collector status unavailable. Refresh after the service is available."}</p>}
      </section>

      <section className={panel} aria-labelledby="settings-title">
        <h2 id="settings-title" className="mb-3 font-semibold">Collection & output settings</h2>
        {settingsError && <div role="alert" className="mb-3 text-sm text-red-400">{settingsError} <button className={button} onClick={() => void loadSettings()}>Retry settings</button></div>}
        <form onSubmit={(event) => { event.preventDefault(); void save(); }} className="flex flex-col gap-4">
          <fieldset disabled={!settingsLoaded || !!busy} className="flex flex-col gap-4 disabled:opacity-60">
            <Toggle label="Enable scheduled collection" checked={settings.enabled} onChange={(enabled) => setSettings((previous) => ({ ...previous, enabled }))} />
            <div className="grid gap-3 sm:grid-cols-2">
              <label className="flex flex-col gap-1 text-xs text-slate-400">Target YouTube account
                <select className={input} value={settings.target_account_id} onChange={(event) => setSettings((previous) => ({ ...previous, target_account_id: Number(event.target.value) }))}>
                  <option value={0}>Select a connected account</option>
                  {settings.target_account_id > 0 && !connectedAccounts.some((account) => account.id === settings.target_account_id) && <option value={settings.target_account_id} disabled>Account {settings.target_account_id} · not connected</option>}
                  {connectedAccounts.map((account) => <option key={account.id} value={account.id}>{account.name} · {account.remote_account_name || account.remote_account_id}</option>)}
                </select>
                {connectedAccounts.length === 0 && <span className="text-amber-300">
                  Connect and confirm a YouTube account in Accounts before enabling collection.
                </span>}
              </label>
              <label className="flex flex-col gap-1 text-xs text-slate-400">Daily limits timezone
                <input className={input} required value={settings.timezone} onChange={(event) => setSettings((previous) => ({ ...previous, timezone: event.target.value }))} />
              </label>
            </div>
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">{fields(discoveryFields)}</div>
            <div className="grid gap-3 border-t border-slate-700 pt-4 sm:grid-cols-2 xl:grid-cols-3">{fields(outputFields)}</div>
          </fieldset>
          <p className="text-xs text-slate-400">Changing the interval does not enable collection. Select a connected account, enable scheduled collection and save settings. Clips are published only after review and scheduling.</p>
          <div className="flex flex-wrap items-center gap-3">
            <button className="rounded-lg bg-indigo-600 px-4 py-2 text-sm hover:bg-indigo-500 disabled:opacity-40" disabled={!settingsLoaded || !!busy}>{busy === "settings" ? "Saving…" : "Save settings"}</button>
            <Link href="/admin/accounts" className="text-sm text-indigo-400">Manage account connections →</Link>
          </div>
        </form>
      </section>

      <section className={panel} aria-labelledby="channels-title">
        <h2 id="channels-title" className="mb-3 font-semibold">Monitored channels</h2>
        <form className="mb-4 flex flex-col gap-2 sm:flex-row" onSubmit={(event) => {
          event.preventDefault(); if (!channelInput.trim()) return;
          void action("add", async () => {
            const channel = await createTwitchChannel(channelInput.trim());
            setChannels((previous) => [...previous.filter((item) => item.id !== channel.id), channel]); setChannelInput(""); setNotice("Channel added.");
          });
        }}>
          <input aria-label="Twitch channel login or URL" className={input} required placeholder="Channel login or https://twitch.tv/channel"
            value={channelInput} onChange={(event) => setChannelInput(event.target.value)} />
          <button className={`${button} shrink-0`} disabled={!!busy || !status?.credentials_ready}>{busy === "add" ? "Adding…" : "Add channel"}</button>
        </form>
        <label className="mb-3 flex items-center gap-2 text-xs text-slate-400"><input type="checkbox" checked={showArchived} onChange={(event) => setShowArchived(event.target.checked)} />Show archived sources</label>
        <div className="flex flex-col gap-3">
          {!channels.filter((channel) => showArchived || !channel.archived).length && <p className="text-sm text-slate-500">No monitored channels.</p>}
          {channels.filter((channel) => showArchived || !channel.archived).map((channel) => <div key={channel.id} className="rounded-lg border border-slate-700 p-3">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <a className="font-medium text-indigo-400" href={`https://www.twitch.tv/${encodeURIComponent(channel.login)}`} target="_blank" rel="noopener noreferrer">{channel.display_name || channel.login}</a>
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <span className="text-slate-400">{channel.archived ? "Archived" : channel.paused ? "Paused" : "Active"}</span>
                {!channel.archived && <>
                  <button className={button} disabled={!!busy} onClick={() => void action(`channel-${channel.id}`, async () => {
                    const updated = await updateTwitchChannel(channel.id, !channel.paused); setChannels((previous) => previous.map((item) => item.id === updated.id ? updated : item));
                  })}>{channel.paused ? "Resume" : "Pause"}</button>
                  <button className={`${button} text-red-300`} disabled={!!busy} onClick={() => setConfirmation({ kind: "archive", channel })}>Archive</button>
                </>}
              </div>
            </div>
            <p className="mt-2 text-xs text-slate-400">Last check: {checkedAt(channel.last_checked_at)}</p>
            {channel.last_error && <p className="mt-2 text-sm text-red-400">{channel.last_error}</p>}
          </div>)}
        </div>
      </section>

      <section className={panel} aria-labelledby="candidates-title">
        <h2 id="candidates-title" className="mb-3 font-semibold">Recent discovered clips</h2>
        {!clips.length && <p className="text-sm text-slate-500">No clips discovered yet.</p>}
        <div className="grid gap-3 lg:grid-cols-2">{clips.map((clip) => <div key={clip.id} className="min-w-0 rounded-lg border border-slate-700 p-3">
          <a href={clip.url} target="_blank" rel="noopener noreferrer" className="break-words text-sm text-indigo-400">{clip.title || clip.id}</a>
          <p className="mt-1 text-xs text-slate-400">{clip.broadcaster_name} · {clip.view_count.toLocaleString()} views · {clip.duration.toFixed(1)}s · {clip.state}</p>
          {clip.last_error && <p className="mt-2 text-xs text-red-400">{clip.last_error}</p>}
        </div>)}</div>
      </section>

      <section className={panel} aria-labelledby="imports-title">
        <div className="mb-3 flex flex-wrap items-center justify-between gap-3"><h2 id="imports-title" className="font-semibold">Imported sources</h2><Link href="/admin/compilations" className="text-sm text-indigo-400">Build a compilation →</Link></div>
        {!imports.length && <p className="text-sm text-slate-500">No imported sources.</p>}
        <div className="grid gap-3 lg:grid-cols-2">{imports.map((item) => <div key={item.import_id} className="min-w-0 rounded-lg border border-slate-700 p-3">
          <p className="break-words text-sm font-medium">{item.source.title || item.source.clip_id}</p>
          <p className="mt-1 text-xs text-slate-400">{item.source.broadcaster_login} · {item.state}{item.rejected ? " · rejected" : " · rights not verified"}</p>
          {item.error && <p className="mt-2 text-xs text-red-400">{item.error}</p>}
          <div className="mt-3 flex flex-wrap items-center gap-3">
            {item.clip_id && <Link href={`/admin/clips/${encodeURIComponent(item.clip_id)}`} className="text-sm text-indigo-400">Review clip →</Link>}
            {!item.rejected && <button className={`${button} text-red-300`} disabled={!!busy} onClick={() => setConfirmation({ kind: "reject", item })}>Reject source</button>}
          </div>
        </div>)}</div>
      </section>
      {confirmation && <ConfirmAction title={confirmation.kind === "archive" ? "Archive Twitch source" : "Reject imported source"}
        message={confirmation.kind === "archive" ? `Stop monitoring ${confirmation.channel.login}? Existing clips remain available for review.` : "Exclude this source from new work? Review any existing publications that already use it."}
        onCancel={() => setConfirmation(null)} onConfirm={() => {
          const pending = confirmation; setConfirmation(null);
          void action(pending.kind, async () => {
            if (pending.kind === "archive") { const updated = await archiveTwitchChannel(pending.channel.id); setChannels((previous) => previous.map((item) => item.id === updated.id ? updated : item)); }
            else { const updated = await rejectImport(pending.item.import_id); setImports((previous) => previous.map((item) => item.import_id === updated.import_id ? updated : item)); }
          });
        }} />}
    </div>
  );
}
