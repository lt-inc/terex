"use client";

import FileDropzone from "@/components/ui/FileDropzone";
import type { AccountCardData } from "@/types";

const PLATFORMS = [
  { value: "youtube",   label: "YouTube" },
  { value: "tiktok",    label: "TikTok" },
  { value: "instagram", label: "Instagram" },
];

interface AccountCardProps {
  card: AccountCardData;
  onChange: (card: AccountCardData) => void;
  onDelete: () => void;
}

export default function AccountCard({ card, onChange, onDelete }: AccountCardProps) {
  function update<K extends keyof AccountCardData>(key: K, value: AccountCardData[K]) {
    onChange({ ...card, [key]: value });
  }

  return (
    <div className="relative bg-slate-800 border border-slate-700 rounded-xl p-5 flex flex-col gap-4">
      <button
        onClick={onDelete}
        className="absolute top-3 right-3 w-7 h-7 flex items-center justify-center rounded-full text-slate-400 hover:text-white hover:bg-slate-700 transition-colors text-lg leading-none"
      >
        &times;
      </button>

      {/* Platform */}
      <div className="flex flex-col gap-1 pr-8">
        <label className="text-xs text-slate-400">Platform</label>
        <select
          value={card.platform}
          onChange={(e) => update("platform", e.target.value)}
          className="bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500"
        >
          <option value="">— select platform —</option>
          {PLATFORMS.map((p) => (
            <option key={p.value} value={p.value}>{p.label}</option>
          ))}
        </select>
      </div>

      {/* Name */}
      <div className="flex flex-col gap-1">
        <label className="text-xs text-slate-400">Name</label>
        <input
          type="text"
          value={card.name}
          onChange={(e) => update("name", e.target.value)}
          placeholder="Account name"
          className="bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-indigo-500"
        />
      </div>

      {/* URL */}
      <div className="flex flex-col gap-1">
        <label className="text-xs text-slate-400">URL</label>
        <input
          type="text"
          value={card.url}
          onChange={(e) => update("url", e.target.value)}
          placeholder="https://..."
          className="bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-indigo-500"
        />
      </div>

      {/* Auth file */}
      <div className="flex flex-col gap-2">
        <label className="text-xs text-slate-400">Auth file</label>
        <FileDropzone
          file={card.authFile}
          onChange={(f) => update("authFile", f)}
        />
      </div>

      {/* Note */}
      <div className="flex flex-col gap-1">
        <label className="text-xs text-slate-400">Note</label>
        <textarea
          value={card.note}
          onChange={(e) => update("note", e.target.value)}
          placeholder="Optional notes..."
          rows={2}
          className="bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-indigo-500 resize-none"
        />
      </div>
    </div>
  );
}
