"use client";

import Toggle from "@/components/ui/Toggle";
import MultiSelect from "@/components/ui/MultiSelect";
import BannerCard from "@/components/upload-video/BannerCard";
import type { VideoCardData, BannerConfig, Platform, AccountsResponse, SliceMode, ApproveMode } from "@/types";

interface VideoCardProps {
  card: VideoCardData;
  accounts: AccountsResponse;
  bannerOptions: string[];
  backgroundOptions: string[];
  onChange: (card: VideoCardData) => void;
  onDelete: () => void;
}

const PLATFORM_LABELS: Record<Platform, string> = {
  youtube: "YouTube",
  tiktok: "TikTok",
  instagram: "Instagram",
};

function createEmptyBanner(): BannerConfig {
  return {
    id: crypto.randomUUID(),
    bannerName: "",
    youtubeAccounts: [],
    tiktokAccounts: [],
    instagramAccounts: [],
    schedules: {},
  };
}

export default function VideoCard({ card, accounts, bannerOptions, backgroundOptions, onChange, onDelete }: VideoCardProps) {
  function update<K extends keyof VideoCardData>(key: K, value: VideoCardData[K]) {
    onChange({ ...card, [key]: value });
  }

  function setBaseAccounts(platform: Platform, selected: string[]) {
    const key = `${platform}Accounts` as "youtubeAccounts" | "tiktokAccounts" | "instagramAccounts";
    onChange({ ...card, [key]: selected });
  }

  function setSchedule(platform: Platform, field: "date" | "time" | "step", value: string) {
    const prev = card.schedules[platform] ?? { date: "", time: "", step: "" };
    onChange({
      ...card,
      schedules: { ...card.schedules, [platform]: { ...prev, [field]: value } },
    });
  }

  function addBanner() {
    onChange({ ...card, banners: [...card.banners, createEmptyBanner()] });
  }

  function updateBanner(id: string, updated: BannerConfig) {
    onChange({ ...card, banners: card.banners.map((b) => (b.id === id ? updated : b)) });
  }

  function deleteBanner(id: string) {
    onChange({ ...card, banners: card.banners.filter((b) => b.id !== id) });
  }

  const activePlatforms: Platform[] = (["youtube", "tiktok", "instagram"] as Platform[]).filter(
    (p) => (card[`${p}Accounts` as "youtubeAccounts" | "tiktokAccounts" | "instagramAccounts"] as string[]).length > 0
  );

  return (
    <div className="relative bg-slate-800 border border-slate-700 rounded-xl p-5 flex flex-col gap-5">
      {/* Delete */}
      <button
        onClick={onDelete}
        className="absolute top-3 right-3 w-7 h-7 flex items-center justify-center rounded-full text-slate-400 hover:text-white hover:bg-slate-700 transition-colors text-lg leading-none"
      >
        &times;
      </button>

      {/* YouTube URL */}
      <div className="flex flex-col gap-1 pr-8">
        <label className="text-xs text-slate-400">YouTube URL</label>
        <input
          type="text"
          value={card.youtubeUrl}
          onChange={(e) => update("youtubeUrl", e.target.value)}
          placeholder="https://youtube.com/watch?v=..."
          className="bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-indigo-500"
        />
      </div>

      {/* Processing toggles */}
      <div className="flex flex-col gap-3">
        <Toggle
          label="Make vertical?"
          checked={card.makeVertical}
          onChange={(v) => onChange({ ...card, makeVertical: v, addBackground: v ? card.addBackground : false })}
        />
        <Toggle
          label="Add subtitles?"
          checked={card.addSubtitles}
          onChange={(v) => update("addSubtitles", v)}
        />
        <div className="flex items-center gap-3 pl-1">
          <span className="text-sm text-slate-300 flex-1">Montage mode</span>
          <select
            value={card.sliceMode}
            onChange={(e) => update("sliceMode", e.target.value as SliceMode)}
            className="bg-slate-700 border border-slate-600 rounded-md px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500"
          >
            <option value="highlights">Highlights only</option>
            <option value="full">Full video (by intervals)</option>
          </select>
        </div>
        <div className="flex items-center gap-3 pl-1">
          <span className="text-sm text-slate-300 flex-1">Publish</span>
          <select
            value={card.approveMode}
            onChange={(e) => update("approveMode", e.target.value as ApproveMode)}
            className="bg-slate-700 border border-slate-600 rounded-md px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500"
          >
            <option value="by_approve">By approve</option>
            <option value="auto_approve">Auto-approve</option>
          </select>
        </div>
        {card.sliceMode === "full" ? (
          <div className="flex items-center gap-3 pl-1">
            <span className="text-sm text-slate-300 flex-1">Min clip duration (sec)</span>
            <input
              type="number"
              min={1}
              value={card.minVideoDuration}
              onChange={(e) => update("minVideoDuration", Number(e.target.value))}
              className="w-24 bg-slate-700 border border-slate-600 rounded-md px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500"
            />
          </div>
        ) : (
          <p className="text-xs text-slate-500 pl-1">AI picks the clips and their length; cuts always land on sentence boundaries.</p>
        )}
        <Toggle
          label="Add background?"
          checked={card.addBackground}
          onChange={(v) => update("addBackground", v)}
          disabled={!card.makeVertical}
        />
        {card.addBackground && (
          <>
            <div className="flex items-center gap-3 pl-1">
              <span className="text-sm text-slate-300 flex-1">Shape</span>
              <div className="flex rounded-lg overflow-hidden border border-slate-600">
                {(["circle", "rectangle"] as const).map((s) => (
                  <button
                    key={s}
                    type="button"
                    onClick={() => update("shape", s)}
                    className={`px-4 py-1.5 text-sm capitalize transition-colors ${
                      card.shape === s ? "bg-indigo-500 text-white" : "bg-slate-700 text-slate-300 hover:bg-slate-600"
                    }`}
                  >
                    {s}
                  </button>
                ))}
              </div>
            </div>
            <div className="flex flex-col gap-1 pl-1">
              <label className="text-xs text-slate-400">Background file</label>
              <select
                value={card.backgroundName}
                onChange={(e) => update("backgroundName", e.target.value)}
                className="bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500"
              >
                <option value="">— select background —</option>
                {backgroundOptions.map((b) => (
                  <option key={b} value={b}>{b}</option>
                ))}
              </select>
            </div>
          </>
        )}
      </div>

      {/* Base account selection — hidden when banners are added */}
      <div className={`flex flex-col gap-3 ${card.banners.length > 0 ? "hidden" : ""}`}>
        <p className="text-xs font-semibold uppercase tracking-widest text-slate-400">Accounts</p>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <MultiSelect label="YouTube" options={accounts.youtube} selected={card.youtubeAccounts} onChange={(v) => setBaseAccounts("youtube", v)} />
          <MultiSelect label="TikTok" options={accounts.tiktok} selected={card.tiktokAccounts} onChange={(v) => setBaseAccounts("tiktok", v)} />
          <MultiSelect label="Instagram" options={accounts.instagram} selected={card.instagramAccounts} onChange={(v) => setBaseAccounts("instagram", v)} />
        </div>

        {/* Publish schedule per active platform */}
        {activePlatforms.length > 0 && (
          <div className="flex flex-col gap-3 mt-1">
            {activePlatforms.map((platform) => {
              const sched = card.schedules[platform] ?? { date: "", time: "", step: "" };
              return (
                <div key={platform} className="flex flex-col gap-1">
                  <p className="text-xs text-slate-400 font-medium">{PLATFORM_LABELS[platform]} publish schedule</p>
                  <div className="flex gap-2">
                    <input
                      type="date"
                      value={sched.date}
                      onChange={(e) => setSchedule(platform, "date", e.target.value)}
                      className="flex-1 bg-slate-700 border border-slate-600 rounded-md px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500 [color-scheme:dark]"
                    />
                    <input
                      type="time"
                      value={sched.time}
                      onChange={(e) => setSchedule(platform, "time", e.target.value)}
                      className="flex-1 bg-slate-700 border border-slate-600 rounded-md px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500 [color-scheme:dark]"
                    />
                    <input
                      type="text"
                      placeholder="Step (e.g. 1d)"
                      value={sched.step}
                      onChange={(e) => setSchedule(platform, "step", e.target.value)}
                      className="flex-1 bg-slate-700 border border-slate-600 rounded-md px-3 py-1.5 text-sm text-slate-100 placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-indigo-500"
                    />
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      {/* Banners */}
      <div className="flex flex-col gap-3">
        {card.banners.map((banner) => (
          <BannerCard
            key={banner.id}
            banner={banner}
            accounts={accounts}
            bannerOptions={bannerOptions}
            onChange={(updated) => updateBanner(banner.id, updated)}
            onDelete={() => deleteBanner(banner.id)}
          />
        ))}
        <button
          type="button"
          onClick={addBanner}
          className="self-start flex items-center gap-1.5 text-sm text-indigo-400 hover:text-indigo-300 transition-colors"
        >
          <span className="text-base leading-none">+</span>
          Add Banner
        </button>
      </div>
    </div>
  );
}
