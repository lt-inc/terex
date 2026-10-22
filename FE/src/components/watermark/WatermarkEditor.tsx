"use client";
import { useEffect, useRef, useState } from "react";
import { fetchWatermarkPreview } from "@/lib/api";
import type { WatermarkPreview, WatermarkRegion } from "@/types";

const MIN = 0.01; // smaller than 1% of the frame is a misclick
const clamp = (v: number) => Math.min(1, Math.max(0, v));

// Modal: stills of the channel's newest video; drag on the picture to mark areas to blur.
export default function WatermarkEditor({
  sourceUrl,
  initial,
  onCancel,
  onDone,
}: {
  sourceUrl: string;
  initial: WatermarkRegion[];
  onCancel: () => void;
  onDone: (regions: WatermarkRegion[]) => void;
}) {
  const [preview, setPreview] = useState<WatermarkPreview | null>(null),
    [error, setError] = useState(""),
    [frame, setFrame] = useState(0),
    [regions, setRegions] = useState<WatermarkRegion[]>(initial),
    [draft, setDraft] = useState<WatermarkRegion | null>(null),
    [blur, setBlur] = useState(false);
  const box = useRef<HTMLDivElement>(null),
    start = useRef<{ x: number; y: number } | null>(null);

  useEffect(() => {
    let live = true;
    fetchWatermarkPreview(sourceUrl)
      .then((p) => live && setPreview(p))
      .catch((e) => live && setError(e instanceof Error ? e.message : String(e)));
    return () => {
      live = false;
    };
  }, [sourceUrl]);

  function point(e: React.PointerEvent) {
    const r = box.current!.getBoundingClientRect();
    return { x: clamp((e.clientX - r.left) / r.width), y: clamp((e.clientY - r.top) / r.height) };
  }
  function span(a: { x: number; y: number }, b: { x: number; y: number }): WatermarkRegion {
    return { x: Math.min(a.x, b.x), y: Math.min(a.y, b.y), w: Math.abs(a.x - b.x), h: Math.abs(a.y - b.y) };
  }
  function down(e: React.PointerEvent) {
    e.currentTarget.setPointerCapture(e.pointerId);
    start.current = point(e);
    setDraft(null);
  }
  function move(e: React.PointerEvent) {
    if (start.current) setDraft(span(start.current, point(e)));
  }
  function up(e: React.PointerEvent) {
    if (!start.current) return;
    const r = span(start.current, point(e));
    start.current = null;
    setDraft(null);
    if (r.w >= MIN && r.h >= MIN) setRegions((p) => [...p, r]);
  }
  const rect = (r: WatermarkRegion) => ({
    left: `${r.x * 100}%`,
    top: `${r.y * 100}%`,
    width: `${r.w * 100}%`,
    height: `${r.h * 100}%`,
  });

  return (
    <div className="fixed inset-0 z-50 bg-black/70 flex items-center justify-center p-4" role="dialog" aria-modal="true" aria-label="Watermark areas">
      <div className="bg-slate-800 border border-slate-700 rounded-xl p-4 w-full max-w-3xl max-h-full overflow-auto flex flex-col gap-3">
        <div className="flex justify-between items-start gap-3">
          <div>
            <h3 className="text-sm font-medium">Mark the areas to blur</h3>
            <p className="text-xs text-slate-400">
              {preview?.video_title ? `Frames of «${preview.video_title}». ` : ""}
              Drag on the picture to add an area. Pick another frame if the watermark is not visible.
            </p>
          </div>
          <button type="button" aria-label="Close" className="text-slate-400 hover:text-white" onClick={onCancel}>
            ×
          </button>
        </div>
        {error && (
          <p role="alert" className="text-red-400 text-sm">
            {error}
          </p>
        )}
        {!preview && !error && <p className="text-sm text-slate-400">Downloading frames… this takes up to a minute.</p>}
        {preview && (
          <>
            <div
              ref={box}
              className="relative select-none touch-none cursor-crosshair self-center max-h-[60vh]"
              style={{ aspectRatio: `${preview.width} / ${preview.height}`, maxWidth: "100%" }}
              onPointerDown={down}
              onPointerMove={move}
              onPointerUp={up}
            >
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={preview.frames[frame]?.image} alt="Video frame" draggable={false} className="w-full h-full object-contain pointer-events-none" />
              {regions.map((r, i) => (
                <div
                  key={i}
                  className="absolute border-2 border-indigo-400 bg-indigo-400/20"
                  style={{ ...rect(r), ...(blur ? { backdropFilter: "blur(12px)", background: "transparent" } : {}) }}
                />
              ))}
              {draft && <div className="absolute border-2 border-dashed border-amber-300" style={rect(draft)} />}
            </div>
            <div className="flex gap-2 overflow-x-auto">
              {preview.frames.map((f, i) => (
                <button
                  type="button"
                  key={i}
                  aria-label={`Frame at ${Math.round(f.time_sec)} s`}
                  onClick={() => setFrame(i)}
                  className={`shrink-0 border-2 rounded ${i === frame ? "border-indigo-400" : "border-transparent"}`}
                >
                  {/* eslint-disable-next-line @next/next/no-img-element */}
                  <img src={f.image} alt="" className="h-14 rounded" />
                </button>
              ))}
            </div>
            <div className="flex flex-wrap items-center gap-3 text-sm">
              <button
                type="button"
                className="text-indigo-400 disabled:opacity-40"
                disabled={!preview.suggested.length}
                onClick={() => setRegions(preview.suggested.map((r) => ({ ...r })))}
              >
                {preview.suggested.length ? `Use detected (${preview.suggested.length})` : "Nothing detected"}
              </button>
              <button type="button" className="text-indigo-400" onClick={() => setBlur(!blur)}>
                {blur ? "Show outlines" : "Preview blur"}
              </button>
              <button type="button" className="text-slate-400 disabled:opacity-40" disabled={!regions.length} onClick={() => setRegions((p) => p.slice(0, -1))}>
                Undo
              </button>
              <button type="button" className="text-red-400 disabled:opacity-40" disabled={!regions.length} onClick={() => setRegions([])}>
                Clear
              </button>
              <span className="text-xs text-slate-500">{regions.length} area(s)</span>
            </div>
          </>
        )}
        <div className="flex justify-end gap-3">
          <button type="button" className="text-sm text-slate-300 px-3 py-2" onClick={onCancel}>
            Cancel
          </button>
          <button
            type="button"
            className="bg-indigo-600 hover:bg-indigo-500 disabled:opacity-40 rounded-lg px-4 py-2 text-sm"
            disabled={!regions.length}
            onClick={() => onDone(regions)}
          >
            Use these areas
          </button>
        </div>
      </div>
    </div>
  );
}
