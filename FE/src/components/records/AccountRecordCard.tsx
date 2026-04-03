"use client";

import { useState } from "react";
import type { AccountRecord } from "@/types";

interface AccountRecordCardProps {
  record: AccountRecord;
  onUpdated: (r: AccountRecord) => void;
  onDeleted: () => void;
  onSave: (id: number, data: Partial<AccountRecord>) => Promise<AccountRecord>;
  onDelete: (id: number) => Promise<void>;
}

const PLATFORMS = ["YouTube", "TikTok", "Instagram"];
const PLATFORM_COLORS: Record<string, string> = {
  YouTube: "bg-red-900/40 text-red-300",
  TikTok: "bg-slate-700 text-slate-300",
  Instagram: "bg-purple-900/40 text-purple-300",
};

export default function AccountRecordCard({ record, onUpdated, onDeleted, onSave, onDelete }: AccountRecordCardProps) {
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [platform, setPlatform] = useState(record.platform);
  const [name, setName] = useState(record.name);
  const [url, setUrl] = useState(record.url ?? "");
  const [note, setNote] = useState(record.note ?? "");

  function reset() {
    setPlatform(record.platform); setName(record.name);
    setUrl(record.url ?? ""); setNote(record.note ?? "");
    setEditing(false);
  }

  async function handleSave() {
    setSaving(true);
    try {
      const updated = await onSave(record.id, { platform, name, url: url || undefined, note: note || undefined });
      onUpdated(updated);
      setEditing(false);
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!confirm(`Delete account "${record.name}"?`)) return;
    await onDelete(record.id);
    onDeleted();
  }

  return (
    <div className="bg-slate-800 border border-slate-700 rounded-xl p-4 flex flex-col gap-3">
      {!editing ? (
        <div className="flex items-start justify-between gap-2">
          <div className="flex flex-col gap-1">
            <div className="flex items-center gap-2">
              <span className={`text-xs font-medium px-2 py-0.5 rounded ${PLATFORM_COLORS[record.platform] ?? "bg-slate-700 text-slate-300"}`}>
                {record.platform}
              </span>
              <span className="text-sm font-medium text-slate-100">{record.name}</span>
            </div>
            {record.url && <a href={record.url} target="_blank" rel="noopener noreferrer" className="text-xs text-indigo-400 hover:underline truncate max-w-sm">{record.url}</a>}
            {record.note && <span className="text-xs text-slate-400">{record.note}</span>}
            {record.file_name && <span className="text-xs text-slate-500 font-mono">Auth: {record.file_name}</span>}
          </div>
          <div className="flex gap-2 shrink-0">
            <button onClick={() => setEditing(true)} className="text-xs text-indigo-400 hover:text-indigo-300 transition-colors">Edit</button>
            <button onClick={handleDelete} className="text-xs text-red-500 hover:text-red-400 transition-colors">Delete</button>
          </div>
        </div>
      ) : (
        <div className="flex flex-col gap-2">
          <div className="grid grid-cols-2 gap-2">
            <div className="flex flex-col gap-1">
              <label className="text-xs text-slate-400">Platform</label>
              <select value={platform} onChange={(e) => setPlatform(e.target.value)}
                className="bg-slate-700 border border-slate-600 rounded-md px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500">
                {PLATFORMS.map((p) => <option key={p} value={p}>{p}</option>)}
              </select>
            </div>
            <div className="flex flex-col gap-1">
              <label className="text-xs text-slate-400">Name</label>
              <input value={name} onChange={(e) => setName(e.target.value)}
                className="bg-slate-700 border border-slate-600 rounded-md px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500" />
            </div>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs text-slate-400">URL</label>
            <input value={url} onChange={(e) => setUrl(e.target.value)}
              className="bg-slate-700 border border-slate-600 rounded-md px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500" />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs text-slate-400">Note</label>
            <input value={note} onChange={(e) => setNote(e.target.value)}
              className="bg-slate-700 border border-slate-600 rounded-md px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500" />
          </div>
          <div className="flex gap-2">
            <button onClick={handleSave} disabled={saving}
              className="bg-indigo-600 hover:bg-indigo-500 disabled:opacity-40 text-white text-xs font-medium px-3 py-1.5 rounded-md transition-colors">
              {saving ? "Saving…" : "Save"}
            </button>
            <button onClick={reset} className="text-xs text-slate-400 hover:text-slate-200 transition-colors px-2">Cancel</button>
          </div>
        </div>
      )}
    </div>
  );
}
