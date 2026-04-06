"use client";

import { useEffect, useState } from "react";
import VideoCard from "@/components/upload-video/VideoCard";
import { fetchAccounts, fetchBanners, fetchBackgrounds, submitJobs } from "@/lib/api";
import type { VideoCardData, AccountsResponse } from "@/types";

function createEmptyCard(): VideoCardData {
  return {
    id: crypto.randomUUID(),
    youtubeUrl: "",
    makeVertical: true,
    addSubtitles: true,
    sliceVideo: true,
    minVideoDuration: 60,
    addBackground: false,
    shape: "rectangle",
    backgroundName: "",
    youtubeAccounts: [],
    tiktokAccounts: [],
    instagramAccounts: [],
    schedules: {},
    banners: [],
  };
}

export default function UploadVideoPage() {
  const [cards, setCards] = useState<VideoCardData[]>([]);
  const [hydrated, setHydrated] = useState(false);
  const [accounts, setAccounts] = useState<AccountsResponse>({
    youtube: [],
    tiktok: [],
    instagram: [],
  });
  const [bannerOptions, setBannerOptions] = useState<string[]>([]);
  const [backgroundOptions, setBackgroundOptions] = useState<string[]>([]);
  const [uploading, setUploading] = useState(false);
  const [toast, setToast] = useState<string | null>(null);

  // load from localStorage after mount (avoids SSR hydration mismatch)
  useEffect(() => {
    try {
      const saved = localStorage.getItem("upload-video-cards");
      if (saved) setCards(JSON.parse(saved));
    } catch {}
    setHydrated(true);
  }, []);

  // persist on every change, but only after hydration
  useEffect(() => {
    if (!hydrated) return;
    localStorage.setItem("upload-video-cards", JSON.stringify(cards));
  }, [cards, hydrated]);

  useEffect(() => {
    fetchAccounts().then(setAccounts).catch(() => console.error("Failed to fetch accounts"));
    fetchBanners().then(setBannerOptions).catch(() => console.error("Failed to fetch banners"));
    fetchBackgrounds().then(setBackgroundOptions).catch(() => console.error("Failed to fetch backgrounds"));
  }, []);

  function addCard() {
    setCards((prev) => [...prev, createEmptyCard()]);
  }

  function updateCard(id: string, updated: VideoCardData) {
    setCards((prev) => prev.map((c) => (c.id === id ? updated : c)));
  }

  function deleteCard(id: string) {
    setCards((prev) => prev.filter((c) => c.id !== id));
  }

  function hasAnyPublicationAccount(card: VideoCardData): boolean {
    const hasBaseAccounts =
      card.youtubeAccounts.length > 0 ||
      card.tiktokAccounts.length > 0 ||
      card.instagramAccounts.length > 0;

    if (hasBaseAccounts) return true;

    return card.banners.some(
      (banner) =>
        banner.youtubeAccounts.length > 0 ||
        banner.tiktokAccounts.length > 0 ||
        banner.instagramAccounts.length > 0
    );
  }

  const canUploadAll =
    cards.length > 0 &&
    cards.every((card) => card.youtubeUrl.trim().length > 0 && hasAnyPublicationAccount(card));

  async function handleUploadAll() {
    if (!canUploadAll) return;
    setUploading(true);
    try {
      const results = await submitJobs(cards);
      const count = results.length;
      setToast(`${count} job${count !== 1 ? "s" : ""} queued successfully`);
      setTimeout(() => setToast(null), 3000);
    } catch {
      setToast("Failed to submit jobs");
      setTimeout(() => setToast(null), 3000);
    } finally {
      setUploading(false);
    }
  }

  return (
    <div className="p-6 flex flex-col gap-6 min-h-full">
      <h1 className="text-xl font-semibold text-slate-100">Upload Video</h1>
      <div className="flex items-center justify-between">
        <button
          onClick={addCard}
          className="flex items-center gap-2 bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-medium px-4 py-2 rounded-lg transition-colors"
        >
          <span className="text-lg leading-none">+</span>
          Add Video
        </button>
        <button
          onClick={handleUploadAll}
          disabled={!canUploadAll || uploading}
          className="flex items-center gap-2 bg-slate-700 hover:bg-slate-600 disabled:opacity-40 disabled:cursor-not-allowed text-white text-sm font-medium px-4 py-2 rounded-lg border border-slate-600 transition-colors"
        >
          {uploading ? "Uploading…" : "Upload All"}
        </button>
      </div>

      {cards.length === 0 && (
        <div className="flex flex-1 items-center justify-center">
          <p className="text-slate-500 text-sm">No videos yet. Click &quot;Add Video&quot; to get started.</p>
        </div>
      )}

      <div className="flex flex-col gap-4">
        {cards.map((card) => (
          <VideoCard
            key={card.id}
            card={card}
            accounts={accounts}
            bannerOptions={bannerOptions}
            backgroundOptions={backgroundOptions}
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
