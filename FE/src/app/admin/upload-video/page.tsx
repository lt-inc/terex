"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import VideoCard from "@/components/upload-video/VideoCard";
import {
  fetchAccountsList,
  fetchBanners,
  fetchBackgrounds,
  submitJobs,
} from "@/lib/api";
import { validateVideoCard } from "@/lib/validation";
import type { VideoCardData, AccountRecord } from "@/types";
function createEmptyCard(): VideoCardData {
  return {
    id: crypto.randomUUID(),
    youtubeUrl: "",
    makeVertical: true,
    addSubtitles: true,
    sliceMode: "highlights",
    minVideoDuration: 60,
    approveMode: "by_approve",
    addBackground: false,
    shape: "rectangle",
    backgroundName: "",
    youtubeAccounts: [],
    tiktokAccounts: [],
    instagramAccounts: [],
    schedules: {},
    banners: [],
    targets: [],
  };
}
export default function UploadVideoPage() {
  const [cards, setCards] = useState<VideoCardData[]>([]),
    [accounts, setAccounts] = useState<AccountRecord[]>([]),
    [banners, setBanners] = useState<string[]>([]),
    [backgrounds, setBackgrounds] = useState<string[]>([]);
  const [hydrated, setHydrated] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [notice, setNotice] = useState(""),
    [results, setResults] = useState<Record<string, string>>({});
  useEffect(() => {
    try {
      const saved = localStorage.getItem("upload-video-cards");
      if (saved) {
        const data = JSON.parse(saved);
        if (Array.isArray(data)) setCards(data);
      }
    } catch {}
    setHydrated(true);
  }, []);
  useEffect(() => {
    if (hydrated)
      try {
        localStorage.setItem("upload-video-cards", JSON.stringify(cards));
      } catch {}
  }, [cards, hydrated]);
  useEffect(() => {
    let active = true;
    Promise.all([fetchAccountsList(), fetchBanners(), fetchBackgrounds()])
      .then(([a, b, bg]) => {
        if (active) {
          setAccounts(a);
          setBanners(b);
          setBackgrounds(bg);
        }
      })
      .catch((e) => {
        if (active) setError(e.message);
      });
    return () => {
      active = false;
    };
  }, []);
  async function submit() {
    const validation = cards.map(
      (card) => [card.id, validateVideoCard(card)] as const,
    );
    const invalid = validation.filter((x) => x[1]);
    if (invalid.length) {
      setResults(
        Object.fromEntries(
          invalid.map(([id, message]) => [id, message ?? "Invalid video"]),
        ),
      );
      return;
    }
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const res = await submitJobs(cards);
      const successful = new Set(res.filter((r) => r.ok).map((r) => r.id));
      setResults(
        Object.fromEntries(
          res
            .filter((r) => !r.ok)
            .map((r) => [r.id, r.error ?? "Submission failed"]),
        ),
      );
      setCards((prev) => prev.filter((c) => !successful.has(c.id)));
      setNotice(
        `${successful.size} queued. ${res.length - successful.size} need attention.`,
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : "Submission failed");
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="p-6 flex flex-col gap-6 min-h-full">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">Upload video</h1>
        <Link className="text-indigo-400 text-sm" href="/admin/calendar">
          Publication calendar →
        </Link>
      </div>
      <p className="text-sm text-slate-400">
        Each account has its own start and interval. Upload begins at that time;
        YouTube processing may finish later. Overdue approved publications catch
        up automatically.
      </p>
      {error && (
        <p role="alert" className="text-red-400">
          {error}
        </p>
      )}
      {notice && (
        <p role="status" className="text-emerald-400 text-sm">
          {notice}
        </p>
      )}
      <div className="flex justify-between">
        <button
          disabled={busy}
          onClick={() => setCards((p) => [...p, createEmptyCard()])}
          className="bg-indigo-600 hover:bg-indigo-500 disabled:opacity-40 px-4 py-2 rounded-lg text-sm"
        >
          + Add video
        </button>
        <button
          disabled={!cards.length || busy}
          onClick={submit}
          className="bg-slate-700 hover:bg-slate-600 disabled:opacity-40 px-4 py-2 rounded-lg text-sm"
        >
          {busy ? "Submitting…" : "Submit all"}
        </button>
      </div>
      {!cards.length && (
        <p className="text-slate-500 text-sm">Add a video to get started.</p>
      )}
      {cards.map((card) => (
        <div key={card.id}>
          {results[card.id] && (
            <p role="alert" className="text-sm text-red-400 mb-2">
              {results[card.id]}
            </p>
          )}
          {!card.targets && (
            <p className="text-amber-400 text-sm mb-2">
              Older draft: select accounts again to assign independent
              schedules.
            </p>
          )}
          <VideoCard
            card={card}
            accounts={accounts}
            bannerOptions={banners}
            backgroundOptions={backgrounds}
            onChange={(updated) =>
              setCards((p) => p.map((c) => (c.id === card.id ? updated : c)))
            }
            onDelete={() => setCards((p) => p.filter((c) => c.id !== card.id))}
          />
        </div>
      ))}
    </div>
  );
}
