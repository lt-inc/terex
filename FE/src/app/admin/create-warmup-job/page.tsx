"use client";

import { useEffect, useState } from "react";
import WarmupJobFormCard, { createEmptyWarmupCard, encodeMaxVideos } from "@/components/warmup-jobs/WarmupJobFormCard";
import type { WarmupJobFormData } from "@/components/warmup-jobs/WarmupJobFormCard";
import { fetchAccountsList, createWarmupJob } from "@/lib/api";
import type { AccountRecord } from "@/types";

const REPEAT_UNITS = [
  { label: "sec",  factor: 1 },
  { label: "min",  factor: 60 },
  { label: "hour", factor: 3600 },
  { label: "day",  factor: 86400 },
];

export default function CreateWarmupJobPage() {
  const [cards, setCards] = useState<WarmupJobFormData[]>([]);
  const [accounts, setAccounts] = useState<AccountRecord[]>([]);
  const [submitting, setSubmitting] = useState(false);
  const [toast, setToast] = useState<string | null>(null);

  useEffect(() => {
    fetchAccountsList().then(setAccounts).catch(() => {});
  }, []);

  function addCard() {
    setCards((prev) => [...prev, createEmptyWarmupCard()]);
  }

  function updateCard(id: string, updated: WarmupJobFormData) {
    setCards((prev) => prev.map((c) => (c.id === id ? updated : c)));
  }

  function deleteCard(id: string) {
    setCards((prev) => prev.filter((c) => c.id !== id));
  }

  const canSubmit = cards.length > 0 && cards.every((c) => c.accountId !== "");

  async function handleSubmitAll() {
    if (!canSubmit) return;
    setSubmitting(true);
    try {
      await Promise.all(
        cards.map((c) =>
          createWarmupJob({
            account_id: c.accountId as number,
            type: c.type,
            warm_up_datetime: c.warmUpDatetime ? new Date(c.warmUpDatetime).toISOString() : null,
            repeat_step: c.type === "cron" ? c.repeatValue * REPEAT_UNITS[c.repeatUnitIdx].factor : null,
            max_videos: encodeMaxVideos(c.maxVideosMode, c.maxVideosX, c.maxVideosY),
          })
        )
      );
      const count = cards.length;
      setCards([]);
      setToast(`${count} warmup job${count !== 1 ? "s" : ""} created`);
      setTimeout(() => setToast(null), 3000);
    } catch {
      setToast("Failed to create warmup jobs");
      setTimeout(() => setToast(null), 3000);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="p-6 flex flex-col gap-6 min-h-full">
      <h1 className="text-xl font-semibold text-slate-100">Create Warmup Job</h1>

      <div className="flex items-center justify-between">
        <button
          onClick={addCard}
          className="flex items-center gap-2 bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-medium px-4 py-2 rounded-lg transition-colors"
        >
          <span className="text-lg leading-none">+</span>
          Add Job
        </button>
        <button
          onClick={handleSubmitAll}
          disabled={!canSubmit || submitting}
          className="flex items-center gap-2 bg-slate-700 hover:bg-slate-600 disabled:opacity-40 disabled:cursor-not-allowed text-white text-sm font-medium px-4 py-2 rounded-lg border border-slate-600 transition-colors"
        >
          {submitting ? "Creating…" : "Submit All"}
        </button>
      </div>

      {cards.length === 0 && (
        <div className="flex flex-1 items-center justify-center">
          <p className="text-slate-500 text-sm">No jobs yet. Click &quot;Add Job&quot; to get started.</p>
        </div>
      )}

      <div className="flex flex-col gap-4">
        {cards.map((card) => (
          <WarmupJobFormCard
            key={card.id}
            card={card}
            accounts={accounts}
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
