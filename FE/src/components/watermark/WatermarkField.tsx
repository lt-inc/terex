"use client";
import { useState } from "react";
import Toggle from "@/components/ui/Toggle";
import WatermarkEditor from "./WatermarkEditor";
import type { WatermarkOptions, WatermarkRegion } from "@/types";

const btn = "text-sm text-indigo-400 disabled:opacity-40";

// Blur of the source's watermark: off, detected automatically on every video, or drawn by hand on a frame.
export default function WatermarkField({
  value,
  sourceUrl,
  onChange,
}: {
  value?: WatermarkOptions;
  sourceUrl: string;
  onChange: (w: WatermarkOptions | undefined) => void;
}) {
  const [editing, setEditing] = useState(false);
  const mode = value?.mode ?? "off";
  const regions = value?.regions ?? [];
  return (
    <div className="flex flex-col gap-2">
      <Toggle
        label="Blur watermark?"
        checked={mode !== "off"}
        onChange={(on) => onChange(on ? { mode: "auto" } : undefined)}
      />
      {mode !== "off" && (
        <div className="flex flex-col gap-2 pl-3 border-l border-slate-700">
          <div className="flex gap-4 text-sm text-slate-300" role="radiogroup" aria-label="Watermark mode">
            <label className="flex items-center gap-2">
              <input
                type="radio"
                checked={mode === "auto"}
                onChange={() => onChange({ mode: "auto" })}
              />
              Auto-detect on every video
            </label>
            <label className="flex items-center gap-2">
              <input
                type="radio"
                checked={mode === "manual"}
                onChange={() => onChange({ mode: "manual", regions })}
              />
              Draw areas by hand
            </label>
          </div>
          {mode === "manual" && (
            <div className="flex items-center gap-3 text-xs text-slate-400">
              <button
                type="button"
                className={btn}
                disabled={!sourceUrl.trim()}
                onClick={() => setEditing(true)}
              >
                {regions.length ? "Edit areas" : "Draw areas"}
              </button>
              <span>
                {regions.length
                  ? `${regions.length} area(s) will be blurred`
                  : sourceUrl.trim()
                    ? "No areas yet: the template cannot be saved"
                    : "Enter the channel / video URL first"}
              </span>
            </div>
          )}
        </div>
      )}
      {editing && (
        <WatermarkEditor
          sourceUrl={sourceUrl}
          initial={regions}
          onCancel={() => setEditing(false)}
          onDone={(r: WatermarkRegion[]) => {
            onChange({ mode: "manual", regions: r });
            setEditing(false);
          }}
        />
      )}
    </div>
  );
}
