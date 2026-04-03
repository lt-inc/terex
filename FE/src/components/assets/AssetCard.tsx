"use client";

import FileDropzone from "@/components/ui/FileDropzone";
import type { AssetCardData } from "@/types";

interface AssetCardProps {
  card: AssetCardData;
  onChange: (card: AssetCardData) => void;
  onDelete: () => void;
}

export default function AssetCard({ card, onChange, onDelete }: AssetCardProps) {
  function update<K extends keyof AssetCardData>(key: K, value: AssetCardData[K]) {
    onChange({ ...card, [key]: value });
  }

  function handleFile(file: File | null) {
    const updates: Partial<AssetCardData> = { file };
    if (file && !card.name) updates.name = file.name;
    onChange({ ...card, ...updates });
  }

  return (
    <div className="relative bg-slate-800 border border-slate-700 rounded-xl p-5 flex flex-col gap-4">
      <button
        onClick={onDelete}
        className="absolute top-3 right-3 w-7 h-7 flex items-center justify-center rounded-full text-slate-400 hover:text-white hover:bg-slate-700 transition-colors text-lg leading-none"
      >
        &times;
      </button>

      {/* Name */}
      <div className="flex flex-col gap-1 pr-8">
        <label className="text-xs text-slate-400">Name</label>
        <input
          type="text"
          value={card.name}
          onChange={(e) => update("name", e.target.value)}
          placeholder="Display name (auto-filled from filename)"
          className="bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-indigo-500"
        />
      </div>

      {/* File */}
      <div className="flex flex-col gap-1">
        <label className="text-xs text-slate-400">File</label>
        <FileDropzone file={card.file} onChange={handleFile} />
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
