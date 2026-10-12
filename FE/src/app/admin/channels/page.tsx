"use client";
import { useCallback, useEffect, useState } from "react";
import Toggle from "@/components/ui/Toggle";
import AccountTargets from "@/components/publications/AccountTargets";
import BannerCard from "@/components/upload-video/BannerCard";
import {
  fetchChannels,
  createChannel,
  updateChannel,
  deleteChannel,
  fetchAccountsList,
  fetchBanners,
  toUTC7,
} from "@/lib/api";
import { validateTargets } from "@/lib/validation";
import type {
  AccountRecord,
  ApproveMode,
  Channel,
  SliceMode,
  VideoCardData,
} from "@/types";
const input =
  "bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100";
const initial: Partial<VideoCardData> = {
  makeVertical: true,
  addSubtitles: true,
  sliceMode: "highlights",
  minVideoDuration: 60,
  shape: "rectangle",
  targets: [],
  youtubeAccounts: [],
  tiktokAccounts: [],
  instagramAccounts: [],
  schedules: {},
  banners: [],
};
function TemplateFields({
  value,
  onChange,
  accounts,
  bannerOptions,
}: {
  value: Partial<VideoCardData>;
  onChange: (s: Partial<VideoCardData>) => void;
  accounts: AccountRecord[];
  bannerOptions: string[];
}) {
  return (
    <div className="flex flex-col gap-3">
      <label className="flex justify-between items-center text-sm text-slate-300">
        Montage
        <select
          className={input}
          value={value.sliceMode ?? "highlights"}
          onChange={(e) =>
            onChange({ ...value, sliceMode: e.target.value as SliceMode })
          }
        >
          <option value="highlights">Highlights</option>
          <option value="full">Full video by intervals</option>
        </select>
      </label>
      {value.sliceMode === "full" && (
        <label className="flex justify-between items-center text-sm text-slate-300">
          Min seconds
          <input
            type="number"
            min={1}
            className={`${input} w-24`}
            value={value.minVideoDuration ?? 60}
            onChange={(e) =>
              onChange({ ...value, minVideoDuration: Number(e.target.value) })
            }
          />
        </label>
      )}
      <Toggle
        label="Make vertical?"
        checked={value.makeVertical ?? true}
        onChange={(v) => onChange({ ...value, makeVertical: v })}
      />
      <Toggle
        label="Add subtitles?"
        checked={value.addSubtitles ?? true}
        onChange={(v) => onChange({ ...value, addSubtitles: v })}
      />
      {!value.banners?.length && (
        <AccountTargets
          accounts={accounts}
          targets={value.targets ?? []}
          requireTime={false}
          onChange={(targets) => onChange({ ...value, targets })}
        />
      )}
      {(value.banners ?? []).map((banner) => (
        <BannerCard
          key={banner.id}
          banner={banner}
          accounts={accounts}
          bannerOptions={bannerOptions}
          requireTime={false}
          onChange={(updated) =>
            onChange({
              ...value,
              banners: value.banners?.map((b) =>
                b.id === banner.id ? updated : b,
              ),
            })
          }
          onDelete={() =>
            onChange({
              ...value,
              banners: value.banners?.filter((b) => b.id !== banner.id),
            })
          }
        />
      ))}
      <button
        className="text-sm text-indigo-400 self-start"
        onClick={() =>
          onChange({
            ...value,
            banners: [
              ...(value.banners ?? []),
              {
                id: crypto.randomUUID(),
                bannerName: "",
                targets: [],
                youtubeAccounts: [],
                tiktokAccounts: [],
                instagramAccounts: [],
                schedules: {},
              },
            ],
          })
        }
      >
        + Add banner variant
      </button>
    </div>
  );
}
export default function ChannelsPage() {
  const [channels, setChannels] = useState<Channel[]>([]),
    [accounts, setAccounts] = useState<AccountRecord[]>([]),
    [name, setName] = useState(""),
    [url, setUrl] = useState(""),
    [approve, setApprove] = useState<ApproveMode>("by_approve"),
    [spec, setSpec] = useState(initial),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [notice, setNotice] = useState("");
  const [bannerOptions, setBannerOptions] = useState<string[]>([]);
  const load = useCallback(async () => {
    setError("");
    try {
      const [c, a, b] = await Promise.all([
        fetchChannels(),
        fetchAccountsList(),
        fetchBanners(),
      ]);
      setChannels(c);
      setAccounts(a);
      setBannerOptions(b);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);
  useEffect(() => {
    load();
  }, [load]);
  async function add() {
    const invalid = templateError(spec);
    if (invalid) {
      setError(invalid);
      return;
    }
    setBusy(true);
    setError("");
    try {
      await createChannel({ name, url, approve_mode: approve, spec });
      setName("");
      setUrl("");
      setNotice("Channel added. Only new videos will be processed.");
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="p-6 flex flex-col gap-5">
      <div className="flex justify-between">
        <h1 className="text-xl font-semibold">Channel monitoring</h1>
        <button className="text-sm text-indigo-400" onClick={load}>
          Refresh
        </button>
      </div>
      {error && (
        <p role="alert" className="text-red-400 text-sm">
          {error}
        </p>
      )}
      {notice && (
        <p role="status" className="text-emerald-400 text-sm">
          {notice}
        </p>
      )}
      <div className="bg-slate-800 border border-slate-700 rounded-xl p-4 max-w-3xl flex flex-col gap-3">
        <h2 className="text-sm font-medium">Add channel</h2>
        <input
          aria-label="Channel name"
          className={input}
          placeholder="Name"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <input
          aria-label="YouTube channel URL"
          className={input}
          placeholder="https://youtube.com/@handle"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
        />
        <select
          aria-label="Approval policy"
          className={input}
          value={approve}
          onChange={(e) => setApprove(e.target.value as ApproveMode)}
        >
          <option value="by_approve">Manual approval</option>
          <option value="auto_approve">Automatic approval</option>
        </select>
        <TemplateFields
          accounts={accounts}
          value={spec}
          onChange={setSpec}
          bannerOptions={bannerOptions}
        />
        <button
          className="self-start bg-indigo-600 hover:bg-indigo-500 disabled:opacity-40 px-4 py-2 rounded-lg text-sm"
          disabled={busy || !url.trim()}
          onClick={add}
        >
          {busy ? "Adding…" : "Add channel"}
        </button>
      </div>
      {channels.map((c) => (
        <ChannelSettings
          key={c.id}
          channel={c}
          accounts={accounts}
          bannerOptions={bannerOptions}
          onUpdated={(u) =>
            setChannels((p) => p.map((x) => (x.id === u.id ? u : x)))
          }
          onDeleted={() => setChannels((p) => p.filter((x) => x.id !== c.id))}
        />
      ))}
      {!channels.length && (
        <p className="text-sm text-slate-500">No monitored channels.</p>
      )}
    </div>
  );
}
function ChannelSettings({
  channel,
  accounts,
  onUpdated,
  onDeleted,
  bannerOptions,
}: {
  channel: Channel;
  accounts: AccountRecord[];
  onUpdated: (c: Channel) => void;
  onDeleted: () => void;
  bannerOptions: string[];
}) {
  const [open, setOpen] = useState(false),
    [spec, setSpec] = useState(channel.spec),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  async function patch(data: Parameters<typeof updateChannel>[1]) {
    setBusy(true);
    setError("");
    try {
      onUpdated(await updateChannel(channel.id, data));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  async function remove() {
    if (!confirm(`Stop monitoring ${channel.name}?`)) return;
    setBusy(true);
    try {
      await deleteChannel(channel.id);
      onDeleted();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  async function save() {
    const invalid = templateError(spec);
    if (invalid) {
      setError(invalid);
      return;
    }
    await patch({ spec });
  }
  return (
    <div className="bg-slate-800 border border-slate-700 rounded-xl p-4 flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-4">
        <div className="flex-1">
          <h2 className="text-sm font-medium">{channel.name}</h2>
          <a
            href={channel.url}
            target="_blank"
            rel="noopener noreferrer"
            className="text-indigo-400 text-xs"
          >
            {channel.url}
          </a>
          <p className="text-xs text-slate-500">
            Last checked:{" "}
            {channel.last_checked_at
              ? toUTC7(channel.last_checked_at) + " UTC+7"
              : "never"}
          </p>
          {channel.last_feed_error && (
            <p className="mt-1 text-xs text-amber-300" role="status">
              {channel.last_feed_error}
              {channel.next_feed_check_at && (
                <> Следующая проверка: {toUTC7(channel.next_feed_check_at)} UTC+7.</>
              )}
            </p>
          )}
        </div>
        <select
          disabled={busy}
          aria-label="Approval mode"
          className={input}
          value={channel.approve_mode}
          onChange={(e) =>
            patch({ approve_mode: e.target.value as ApproveMode })
          }
        >
          <option value="by_approve">Manual approval</option>
          <option value="auto_approve">Automatic approval</option>
        </select>
        <Toggle
          label="Enabled"
          checked={channel.enabled}
          disabled={busy}
          onChange={(v) => patch({ enabled: v })}
        />
        <button
          className="text-indigo-400 text-xs"
          onClick={() => {
            setSpec(channel.spec);
            setOpen(!open);
          }}
        >
          Template
        </button>
        <button
          disabled={busy}
          className="text-red-400 text-xs"
          onClick={remove}
        >
          Delete
        </button>
      </div>
      {error && (
        <p role="alert" className="text-red-400 text-sm">
          {error}
        </p>
      )}
      {open && (
        <div className="max-w-3xl border-t border-slate-700 pt-3">
          <p className="text-xs text-slate-400 mb-3">
            These settings apply to future videos. Each destination has an
            independent schedule.
          </p>
          <TemplateFields
            value={spec}
            onChange={setSpec}
            accounts={accounts}
            bannerOptions={bannerOptions}
          />
          <button
            disabled={busy}
            className="mt-3 bg-indigo-600 disabled:opacity-40 rounded-lg px-3 py-2 text-sm"
            onClick={save}
          >
            Save template
          </button>
        </div>
      )}
    </div>
  );
}
function templateError(spec: Partial<VideoCardData>): string | null {
  if (spec.banners?.length) {
    for (const banner of spec.banners) {
      if (!banner.bannerName) return "Select a file for every banner variant";
      const error = validateTargets(banner.targets ?? [], false);
      if (error) return error;
    }
    return null;
  }
  return validateTargets(spec.targets ?? [], false);
}
