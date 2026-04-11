"use client";

import { useState } from "react";
import FileDropzone from "@/components/ui/FileDropzone";
import { captureAuthStart, captureAuthFinish } from "@/lib/api";
import type { AccountCardData } from "@/types";

const PLATFORMS = [
  { value: "youtube",   label: "YouTube" },
  { value: "tiktok",    label: "TikTok" },
  { value: "instagram", label: "Instagram" },
];
const NOVNC_URL = "/novnc/vnc.html?path=novnc";

type CaptureState = "idle" | "starting" | "active" | "finishing" | "done" | "error";

interface AccountCardProps {
  card: AccountCardData;
  onChange: (card: AccountCardData) => void;
  onDelete: () => void;
}

export default function AccountCard({ card, onChange, onDelete }: AccountCardProps) {
  const [captureState, setCaptureState] = useState<CaptureState>("idle");
  const [captureError, setCaptureError] = useState<string | null>(null);

  function update<K extends keyof AccountCardData>(key: K, value: AccountCardData[K]) {
    onChange({ ...card, [key]: value });
  }

  const isYouTube = card.platform === "youtube";
  const isTikTok = card.platform === "tiktok";
  const canCapture = (isYouTube || isTikTok) && card.name.trim().length > 0;

  async function handleStartCapture() {
    setCaptureState("starting");
    setCaptureError(null);
    try {
      await captureAuthStart(card.platform, card.name.trim());
      setCaptureState("active");
      window.open(NOVNC_URL, "_blank");
    } catch (e) {
      setCaptureError(String(e));
      setCaptureState("error");
    }
  }

  async function handleFinishCapture() {
    setCaptureState("finishing");
    setCaptureError(null);
    try {
      const result = await captureAuthFinish();
      onChange({ ...card, capturedFileName: result.file_name });
      setCaptureState("done");
    } catch (e) {
      setCaptureError(String(e));
      setCaptureState("active");
    }
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

        {/* Capture Auth via noVNC — YouTube and TikTok */}
        {(isYouTube || isTikTok) && (
          <div className="flex flex-col gap-2 pt-1">
            {captureState === "idle" || captureState === "error" ? (
              <button
                type="button"
                onClick={handleStartCapture}
                disabled={!canCapture}
                className={`flex items-center gap-2 text-xs font-medium disabled:opacity-40 disabled:cursor-not-allowed text-white px-3 py-1.5 rounded-md transition-colors w-fit ${isTikTok ? "bg-slate-900 hover:bg-slate-700 border border-slate-600" : "bg-red-700 hover:bg-red-600"}`}
              >
                <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 10l4.553-2.069A1 1 0 0121 8.87v6.26a1 1 0 01-1.447.894L15 14M5 18h8a2 2 0 002-2V8a2 2 0 00-2-2H5a2 2 0 00-2 2v8a2 2 0 002 2z" />
                </svg>
                Capture Auth via noVNC
              </button>
            ) : captureState === "starting" ? (
              <span className="text-xs text-slate-400">Starting browser…</span>
            ) : captureState === "active" ? (
              <div className="flex items-center gap-2">
                <span className="w-2 h-2 rounded-full bg-green-400 animate-pulse" />
                <span className="text-xs text-green-400">Browser active —</span>
                <a href={NOVNC_URL} target="_blank" rel="noopener noreferrer" className="text-xs text-indigo-400 hover:underline">Open noVNC</a>
                <button
                  type="button"
                  onClick={handleFinishCapture}
                  className="ml-2 text-xs font-medium bg-indigo-600 hover:bg-indigo-500 text-white px-3 py-1 rounded-md transition-colors"
                >
                  Done — capture
                </button>
              </div>
            ) : captureState === "finishing" ? (
              <span className="text-xs text-slate-400">Capturing…</span>
            ) : captureState === "done" ? (
              <div className="flex items-center gap-2">
                <span className="text-xs text-emerald-400">✓ Auth captured:</span>
                <span className="text-xs font-mono text-slate-300">{card.capturedFileName}</span>
              </div>
            ) : null}

            {captureError && <span className="text-xs text-red-400">{captureError}</span>}
            {!canCapture && captureState === "idle" && (
              <span className="text-xs text-slate-500">Fill in the name first to capture auth</span>
            )}
          </div>
        )}
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
