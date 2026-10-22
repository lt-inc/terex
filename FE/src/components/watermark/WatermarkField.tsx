"use client";
import { useState } from "react";
import Toggle from "@/components/ui/Toggle";
import WatermarkEditor from "./WatermarkEditor";
import { TEXTS, type Lang } from "./texts";
import type { WatermarkOptions, WatermarkRegion } from "@/types";

const btn = "text-sm text-indigo-400 disabled:opacity-40";

// Blur of the source's watermark: off, detected automatically on every video, or drawn by hand on a frame.
export default function WatermarkField({
  value,
  sourceUrl,
  onChange,
  lang = "en",
}: {
  value?: WatermarkOptions;
  sourceUrl: string;
  onChange: (w: WatermarkOptions | undefined) => void;
  lang?: Lang;
}) {
  const t = TEXTS[lang];
  const [editing, setEditing] = useState(false);
  const mode = value?.mode ?? "off";
  const regions = value?.regions ?? [];
  return (
    <div className="flex flex-col gap-2">
      <Toggle
        label={t.toggle}
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
              {t.auto}
            </label>
            <label className="flex items-center gap-2">
              <input
                type="radio"
                checked={mode === "manual"}
                onChange={() => onChange({ mode: "manual", regions })}
              />
              {t.manual}
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
                {regions.length ? t.edit : t.draw}
              </button>
              <span>
                {regions.length
                  ? t.count(regions.length)
                  : sourceUrl.trim()
                    ? t.none
                    : t.needUrl}
              </span>
            </div>
          )}
        </div>
      )}
      {editing && (
        <WatermarkEditor
          sourceUrl={sourceUrl}
          lang={lang}
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
