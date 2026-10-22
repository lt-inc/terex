"use client";
import Toggle from "@/components/ui/Toggle";
import AccountTargets from "@/components/publications/AccountTargets";
import WatermarkField from "@/components/watermark/WatermarkField";
import BannerCard from "./BannerCard";
import type {
  VideoCardData,
  AccountRecord,
  SliceMode,
  ApproveMode,
} from "@/types";
export default function VideoCard({
  card,
  accounts,
  bannerOptions,
  backgroundOptions,
  onChange,
  onDelete,
}: {
  card: VideoCardData;
  accounts: AccountRecord[];
  bannerOptions: string[];
  backgroundOptions: string[];
  onChange: (c: VideoCardData) => void;
  onDelete: () => void;
}) {
  const cls =
    "bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100";
  function update<K extends keyof VideoCardData>(
    key: K,
    value: VideoCardData[K],
  ) {
    onChange({ ...card, [key]: value });
  }
  return (
    <div className="relative bg-slate-800 border border-slate-700 rounded-xl p-5 flex flex-col gap-4">
      <button
        onClick={onDelete}
        aria-label="Delete video"
        className="absolute top-3 right-4 text-slate-400 hover:text-red-400"
      >
        ×
      </button>
      <label className="flex flex-col gap-1 pr-8 text-xs text-slate-400">
        YouTube URL
        <input
          className={cls}
          value={card.youtubeUrl}
          onChange={(e) => update("youtubeUrl", e.target.value)}
          placeholder="https://youtube.com/watch?v=..."
        />
      </label>
      <Toggle
        label="Make vertical?"
        checked={card.makeVertical}
        onChange={(v) =>
          onChange({
            ...card,
            makeVertical: v,
            addBackground: v && card.addBackground,
          })
        }
      />
      <Toggle
        label="Add subtitles?"
        checked={card.addSubtitles}
        onChange={(v) => update("addSubtitles", v)}
      />
      <div className="flex flex-wrap gap-4">
        <label className="flex flex-col gap-1 text-xs text-slate-400">
          Montage
          <select
            className={cls}
            value={card.sliceMode}
            onChange={(e) => update("sliceMode", e.target.value as SliceMode)}
          >
            <option value="highlights">Highlights</option>
            <option value="full">Full video by intervals</option>
          </select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-slate-400">
          Approval
          <select
            className={cls}
            value={card.approveMode}
            onChange={(e) =>
              update("approveMode", e.target.value as ApproveMode)
            }
          >
            <option value="by_approve">Manual approval</option>
            <option value="auto_approve">Automatic approval</option>
          </select>
        </label>
        {card.sliceMode === "full" && (
          <label className="flex flex-col gap-1 text-xs text-slate-400">
            Minimum seconds
            <input
              className={`${cls} w-24`}
              type="number"
              min={1}
              value={card.minVideoDuration}
              onChange={(e) =>
                update("minVideoDuration", Number(e.target.value))
              }
            />
          </label>
        )}
      </div>
      <WatermarkField
        value={card.watermark}
        sourceUrl={card.youtubeUrl}
        onChange={(w) => update("watermark", w)}
      />
      <Toggle
        label="Add background?"
        checked={card.addBackground}
        disabled={!card.makeVertical}
        onChange={(v) => update("addBackground", v)}
      />
      {card.addBackground && (
        <div className="flex gap-3">
          <select
            aria-label="Background"
            className={cls}
            value={card.backgroundName}
            onChange={(e) => update("backgroundName", e.target.value)}
          >
            <option value="">Select background</option>
            {backgroundOptions.map((b) => (
              <option key={b}>{b}</option>
            ))}
          </select>
          <select
            aria-label="Shape"
            className={cls}
            value={card.shape}
            onChange={(e) =>
              update("shape", e.target.value as "circle" | "rectangle")
            }
          >
            <option value="rectangle">Rectangle</option>
            <option value="circle">Circle</option>
          </select>
        </div>
      )}
      {card.banners.length === 0 && (
        <AccountTargets
          accounts={accounts}
          targets={card.targets ?? []}
          onChange={(t) => update("targets", t)}
        />
      )}
      {card.banners.map((b) => (
        <BannerCard
          key={b.id}
          banner={b}
          accounts={accounts}
          bannerOptions={bannerOptions}
          onChange={(updated) =>
            update(
              "banners",
              card.banners.map((x) => (x.id === b.id ? updated : x)),
            )
          }
          onDelete={() =>
            update(
              "banners",
              card.banners.filter((x) => x.id !== b.id),
            )
          }
        />
      ))}
      <button
        className="self-start text-indigo-400 text-sm"
        onClick={() =>
          update("banners", [
            ...card.banners,
            {
              id: crypto.randomUUID(),
              bannerName: "",
              youtubeAccounts: [],
              tiktokAccounts: [],
              instagramAccounts: [],
              schedules: {},
              targets: [],
            },
          ])
        }
      >
        + Add banner variant
      </button>
    </div>
  );
}
