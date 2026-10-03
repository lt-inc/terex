"use client";
import AccountTargets from "@/components/publications/AccountTargets";
import type { BannerConfig, AccountRecord } from "@/types";
export default function BannerCard({
  banner,
  accounts,
  bannerOptions,
  onChange,
  onDelete,
  requireTime = true,
}: {
  banner: BannerConfig;
  accounts: AccountRecord[];
  bannerOptions: string[];
  onChange: (b: BannerConfig) => void;
  onDelete: () => void;
  requireTime?: boolean;
}) {
  return (
    <div className="relative p-4 bg-slate-900 border border-slate-600 rounded-lg flex flex-col gap-3">
      <button
        onClick={onDelete}
        aria-label="Delete banner"
        className="absolute top-2 right-3 text-slate-400 hover:text-red-400"
      >
        ×
      </button>
      <label className="flex flex-col gap-1 text-xs text-slate-400">
        Banner file
        <select
          className="bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100"
          value={banner.bannerName}
          onChange={(e) => onChange({ ...banner, bannerName: e.target.value })}
        >
          <option value="">Select banner</option>
          {bannerOptions.map((b) => (
            <option key={b}>{b}</option>
          ))}
        </select>
      </label>
      <AccountTargets
        accounts={accounts}
        targets={banner.targets ?? []}
        onChange={(targets) => onChange({ ...banner, targets })}
        requireTime={requireTime}
      />
    </div>
  );
}
