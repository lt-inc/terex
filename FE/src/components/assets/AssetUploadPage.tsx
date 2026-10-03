"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import AssetCard from "@/components/assets/AssetCard";
import { uploadAsset } from "@/lib/api";
import type { AssetCardData } from "@/types";
export default function AssetUploadPage({
  kind,
}: {
  kind: "banners" | "backgrounds";
}) {
  const [cards, setCards] = useState<AssetCardData[]>([]),
    [hydrated, setHydrated] = useState(false),
    [busy, setBusy] = useState(false),
    [errors, setErrors] = useState<Record<string, string>>({}),
    [notice, setNotice] = useState("");
  useEffect(() => {
    try {
      const saved = localStorage.getItem(`${kind}-cards`);
      if (saved) {
        const parsed = JSON.parse(saved);
        if (Array.isArray(parsed))
          setCards(parsed.map((c) => ({ ...c, file: null })));
      }
    } catch {}
    setHydrated(true);
  }, [kind]);
  useEffect(() => {
    if (hydrated)
      try {
        localStorage.setItem(
          `${kind}-cards`,
          JSON.stringify(cards.map(({ file: _, ...rest }) => rest)),
        );
      } catch {}
  }, [cards, hydrated, kind]);
  async function submit() {
    setBusy(true);
    setNotice("");
    const results = await Promise.all(
      cards.map(async (card) => {
        if (!card.file || !card.name.trim())
          return { id: card.id, error: "Choose a file and a name" };
        try {
          await uploadAsset(kind, card.name.trim(), card.note, card.file);
          return { id: card.id, error: "" };
        } catch (e) {
          return {
            id: card.id,
            error: e instanceof Error ? e.message : String(e),
          };
        }
      }),
    );
    const success = new Set(results.filter((r) => !r.error).map((r) => r.id));
    setErrors(
      Object.fromEntries(
        results.filter((r) => r.error).map((r) => [r.id, r.error]),
      ),
    );
    setCards((p) => p.filter((c) => !success.has(c.id)));
    setNotice(
      `${success.size} uploaded. ${results.length - success.size} need attention.`,
    );
    setBusy(false);
  }
  return (
    <div className="p-6 flex flex-col gap-5">
      <div className="flex justify-between">
        <h1 className="text-xl font-semibold">Create {kind}</h1>
        <Link className="text-indigo-400 text-sm" href={`/admin/${kind}-list`}>
          View files →
        </Link>
      </div>
      <p className="text-xs text-slate-500">
        Draft details are saved locally. Choose files again after reloading.
      </p>
      <div className="flex justify-between">
        <button
          disabled={busy}
          className="bg-indigo-600 disabled:opacity-40 px-4 py-2 rounded-lg text-sm"
          onClick={() =>
            setCards((p) => [
              ...p,
              { id: crypto.randomUUID(), name: "", file: null, note: "" },
            ])
          }
        >
          + Add file
        </button>
        <button
          className="bg-slate-700 disabled:opacity-40 px-4 py-2 rounded-lg text-sm"
          disabled={busy || !cards.length}
          onClick={submit}
        >
          {busy ? "Uploading…" : "Upload all"}
        </button>
      </div>
      {notice && (
        <p role="status" className="text-emerald-400 text-sm">
          {notice}
        </p>
      )}
      {cards.map((card) => (
        <div key={card.id}>
          {errors[card.id] && (
            <p role="alert" className="text-red-400 text-sm mb-2">
              {errors[card.id]}
            </p>
          )}
          <AssetCard
            card={card}
            onChange={(u) =>
              setCards((p) => p.map((c) => (c.id === card.id ? u : c)))
            }
            onDelete={() => setCards((p) => p.filter((c) => c.id !== card.id))}
          />
        </div>
      ))}
    </div>
  );
}
