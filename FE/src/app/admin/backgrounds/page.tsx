"use client";

import { useEffect, useState } from "react";
import AssetCard from "@/components/assets/AssetCard";
import { uploadAsset } from "@/lib/api";
import type { AssetCardData } from "@/types";

type SerializableCard = Omit<AssetCardData, "file">;

function createEmptyCard(): AssetCardData {
  return { id: crypto.randomUUID(), name: "", file: null, note: "" };
}

export default function BackgroundsPage() {
  const [cards, setCards] = useState<AssetCardData[]>([]);
  const [hydrated, setHydrated] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [toast, setToast] = useState<string | null>(null);

  useEffect(() => {
    try {
      const saved = localStorage.getItem("backgrounds-cards");
      if (saved) {
        const parsed: SerializableCard[] = JSON.parse(saved);
        setCards(parsed.map((c) => ({ ...c, file: null })));
      }
    } catch {}
    setHydrated(true);
  }, []);

  useEffect(() => {
    if (!hydrated) return;
    const serializable: SerializableCard[] = cards.map(({ file: _, ...rest }) => rest);
    localStorage.setItem("backgrounds-cards", JSON.stringify(serializable));
  }, [cards, hydrated]);

  function addCard() {
    setCards((prev) => [...prev, createEmptyCard()]);
  }

  function updateCard(id: string, updated: AssetCardData) {
    setCards((prev) => prev.map((c) => (c.id === id ? updated : c)));
  }

  function deleteCard(id: string) {
    setCards((prev) => prev.filter((c) => c.id !== id));
  }

  async function handleUploadAll() {
    if (cards.length === 0) return;
    setUploading(true);
    try {
      await Promise.all(
        cards.map((card) => {
          if (!card.file) throw new Error("Choose a file for every card");
          return uploadAsset("backgrounds", card.name, card.note, card.file);
        })
      );
      setToast(`${cards.length} background${cards.length !== 1 ? "s" : ""} uploaded`);
      setTimeout(() => setToast(null), 3000);
    } catch {
      setToast("Failed to upload backgrounds");
      setTimeout(() => setToast(null), 3000);
    } finally {
      setUploading(false);
    }
  }

  return (
    <div className="p-6 flex flex-col gap-6 min-h-full">
      <h1 className="text-xl font-semibold text-slate-100">Create Backgrounds</h1>
      <div className="flex items-center justify-between">
        <button
          onClick={addCard}
          className="flex items-center gap-2 bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-medium px-4 py-2 rounded-lg transition-colors"
        >
          <span className="text-lg leading-none">+</span>
          Add Background
        </button>
        <button
          onClick={handleUploadAll}
          disabled={cards.length === 0 || uploading}
          className="flex items-center gap-2 bg-slate-700 hover:bg-slate-600 disabled:opacity-40 disabled:cursor-not-allowed text-white text-sm font-medium px-4 py-2 rounded-lg border border-slate-600 transition-colors"
        >
          {uploading ? "Uploading…" : "Upload All"}
        </button>
      </div>

      {cards.length === 0 && (
        <div className="flex flex-1 items-center justify-center">
          <p className="text-slate-500 text-sm">No backgrounds yet. Click &quot;Add Background&quot; to get started.</p>
        </div>
      )}

      <div className="flex flex-col gap-4">
        {cards.map((card) => (
          <AssetCard
            key={card.id}
            card={card}
            onChange={(updated) => updateCard(card.id, updated)}
            onDelete={() => deleteCard(card.id)}
          />
        ))}
      </div>

      {toast && (
        <div className="fixed bottom-6 right-6 bg-indigo-600 text-white text-sm px-4 py-3 rounded-lg shadow-lg z-50">
          {toast}
        </div>
      )}
    </div>
  );
}
