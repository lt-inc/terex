"use client";

import { useState } from "react";
import type { AssetRecord } from "@/types";

interface AssetRecordCardProps {
  record: AssetRecord;
  onUpdated: (r: AssetRecord) => void;
  onDeleted: () => void;
  onSave: (
    id: number,
    data: { name?: string; note?: string },
  ) => Promise<AssetRecord>;
  onDelete: (id: number) => Promise<void>;
}

function isVideo(name: string) {
  return /\.(mp4|mov|webm|avi)$/i.test(name);
}
function isImage(name: string) {
  return /\.(jpe?g|png|gif|webp|avif)$/i.test(name);
}

export default function AssetRecordCard({
  record,
  onUpdated,
  onDeleted,
  onSave,
  onDelete,
}: AssetRecordCardProps) {
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [name, setName] = useState(record.name);
  const [note, setNote] = useState(record.note ?? "");
  const [error, setError] = useState("");

  function reset() {
    setName(record.name);
    setNote(record.note ?? "");
    setEditing(false);
  }

  async function handleSave() {
    setSaving(true);
    try {
      setError("");
      const updated = await onSave(record.id, { name, note });
      onUpdated(updated);
      setEditing(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!confirm(`Delete "${record.name}"?`)) return;
    try {
      await onDelete(record.id);
      onDeleted();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  // presigned MinIO URL issued by the backend (via gpu-be); the browser fetches the file directly
  const previewUrl = record.preview_url ?? null;

  return (
    <div className="bg-slate-800 border border-slate-700 rounded-xl overflow-hidden flex gap-4 p-4">
      {/* Preview */}
      <div className="shrink-0 w-40 h-28 bg-slate-900 rounded-lg flex items-center justify-center overflow-hidden">
        {previewUrl && isVideo(record.file_name!) ? (
          <video
            src={previewUrl}
            className="w-full h-full object-contain"
            muted
            playsInline
            controls={false}
            onMouseOver={(e) => {
              const v = e.currentTarget as HTMLVideoElement;
              v.play().catch(() => {});
            }}
            onMouseOut={(e) => {
              const v = e.currentTarget as HTMLVideoElement;
              v.pause();
              v.currentTime = 0;
            }}
          />
        ) : previewUrl && isImage(record.file_name!) ? (
          <img
            src={previewUrl}
            className="w-full h-full object-contain"
            alt={record.name}
          />
        ) : (
          <span className="text-xs text-slate-600">
            {record.file_name ?? "no file"}
          </span>
        )}
      </div>

      {/* Info */}
      <div className="flex-1 min-w-0 flex flex-col gap-2">
        {error && (
          <p role="alert" className="text-sm text-red-400">
            {error}
          </p>
        )}
        {!editing ? (
          <>
            <div className="flex items-start justify-between gap-2">
              <div className="flex flex-col gap-0.5">
                <span className="text-sm font-medium text-slate-100">
                  {record.name}
                </span>
                {record.file_name && (
                  <span className="text-xs text-slate-500 font-mono">
                    {record.file_name}
                  </span>
                )}
                {record.note && (
                  <span className="text-xs text-slate-400">{record.note}</span>
                )}
              </div>
              <div className="flex gap-2 shrink-0">
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
              </div>
            </div>
          </>
        ) : (
          <div className="flex flex-col gap-2">
            <div className="flex flex-col gap-1">
              <label className="text-xs text-slate-400">Name</label>
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
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
    </div>
  );
}
