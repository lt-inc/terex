"use client";

import MultiSelect from "@/components/ui/MultiSelect";
import type { BannerConfig, Platform, AccountsResponse } from "@/types";

interface BannerCardProps {
  banner: BannerConfig;
  accounts: AccountsResponse;
  bannerOptions: string[];
  onChange: (banner: BannerConfig) => void;
  onDelete: () => void;
}

const PLATFORM_LABELS: Record<Platform, string> = {
  youtube: "YouTube",
  tiktok: "TikTok",
  instagram: "Instagram",
};

export default function BannerCard({ banner, accounts, bannerOptions, onChange, onDelete }: BannerCardProps) {
  function setAccounts(platform: Platform, selected: string[]) {
    const key = `${platform}Accounts` as keyof BannerConfig;
    onChange({ ...banner, [key]: selected });
  }

  function setSchedule(platform: Platform, field: "date" | "time" | "step", value: string) {
    const prev = banner.schedules[platform] ?? { date: "", time: "", step: "" };
    onChange({
      ...banner,
      schedules: {
        ...banner.schedules,
        [platform]: { ...prev, [field]: value },
      },
    });
  }

  const activePlatforms: Platform[] = (["youtube", "tiktok", "instagram"] as Platform[]).filter((p) => {
    const key = `${p}Accounts` as keyof BannerConfig;
    return (banner[key] as string[]).length > 0;
  });

  return (
    <div className="relative p-4 bg-slate-900 border border-slate-600 rounded-lg">
      {/* Delete banner button */}
      <button
        onClick={onDelete}
        className="absolute top-2 right-2 w-6 h-6 flex items-center justify-center rounded-full text-slate-500 hover:text-white hover:bg-slate-700 transition-colors text-sm leading-none"
        aria-label="Delete banner"
      >
        &times;
      </button>

      <p className="text-xs font-semibold uppercase tracking-widest text-slate-400 mb-3">Banner</p>

      {/* Banner file selector */}
      <div className="flex flex-col gap-1 mb-3">
        <label className="text-xs text-slate-400">Banner file</label>
        <select
          value={banner.bannerName}
          onChange={(e) => onChange({ ...banner, bannerName: e.target.value })}
          className="bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500"
        >
          <option value="">— select banner —</option>
          {bannerOptions.map((b) => (
            <option key={b} value={b}>{b}</option>
          ))}
        </select>
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <MultiSelect
          label="YouTube Account"
          options={accounts.youtube}
          selected={banner.youtubeAccounts}
          onChange={(v) => setAccounts("youtube", v)}
        />
        <MultiSelect
          label="TikTok Account"
          options={accounts.tiktok}
          selected={banner.tiktokAccounts}
          onChange={(v) => setAccounts("tiktok", v)}
        />
        <MultiSelect
          label="Instagram Account"
          options={accounts.instagram}
          selected={banner.instagramAccounts}
          onChange={(v) => setAccounts("instagram", v)}
        />
      </div>

      {activePlatforms.length > 0 && (
        <div className="mt-4 flex flex-col gap-3">
          {activePlatforms.map((platform) => {
            const sched = banner.schedules[platform] ?? { date: "", time: "", step: "" };
            return (
              <div key={platform} className="flex flex-col gap-1">
                <p className="text-xs text-slate-400 font-medium">
                  {PLATFORM_LABELS[platform]} publish schedule
                </p>
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
  );
}
