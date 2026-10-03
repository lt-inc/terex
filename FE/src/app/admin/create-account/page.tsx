"use client";

import { useEffect, useState } from "react";
import AccountCard from "@/components/create-account/AccountCard";
import type { AccountCardData } from "@/types";
import { request } from "@/lib/api";
import Link from "next/link";

function createEmptyCard(): AccountCardData {
  return {
    id: crypto.randomUUID(),
    platform: "",
    name: "",
    url: "",
    authFile: null,
    note: "",
  };
}

type SerializableCard = Omit<AccountCardData, "authFile">;

export default function CreateAccountPage() {
  const [cards, setCards] = useState<AccountCardData[]>([]);
  const [hydrated, setHydrated] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [toast, setToast] = useState<string | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    try {
      const saved = localStorage.getItem("create-account-cards");
      if (saved) {
        const parsed: SerializableCard[] = JSON.parse(saved);
        setCards(parsed.map((c) => ({ ...c, authFile: null })));
      }
    } catch {}
    setHydrated(true);
  }, []);

  useEffect(() => {
    if (!hydrated) return;
    const serializable: SerializableCard[] = cards.map(
      ({ authFile: _, ...rest }) => rest,
    );
    localStorage.setItem("create-account-cards", JSON.stringify(serializable));
  }, [cards, hydrated]);

  function addCard() {
    setCards((prev) => [...prev, createEmptyCard()]);
  }

  function updateCard(id: string, updated: AccountCardData) {
    setCards((prev) => prev.map((c) => (c.id === id ? updated : c)));
  }

  function deleteCard(id: string) {
    setCards((prev) => prev.filter((c) => c.id !== id));
  }

  async function handleSubmitAll() {
    if (cards.length === 0) return;
    setSubmitting(true);
    try {
      const results = await Promise.all(
        cards.map(async (card) => {
          if (!card.name.trim() || !card.platform)
            return { id: card.id, error: "Choose a platform and account name" };
          const form = new FormData();
          form.append("platform", card.platform);
          form.append("name", card.name);
          form.append("url", card.url);
          form.append("note", card.note);
          if (card.authFile) form.append("auth_file", card.authFile);
          try {
            await request("/accounts/create", { method: "POST", body: form });
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
      setCards((prev) => prev.filter((c) => !success.has(c.id)));
      setToast(`${success.size} saved. Open Accounts to connect with Google.`);
      setTimeout(() => setToast(null), 3000);
    } catch {
      setToast("Failed to create accounts");
      setTimeout(() => setToast(null), 3000);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="p-6 flex flex-col gap-6 min-h-full">
      <h1 className="text-xl font-semibold text-slate-100">Create Account</h1>
      <Link href="/admin/accounts" className="text-indigo-400 text-sm">
        Accounts and Google connections →
      </Link>
      <div className="flex items-center justify-between">
        <button
          onClick={addCard}
          className="flex items-center gap-2 bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-medium px-4 py-2 rounded-lg transition-colors"
        >
          <span className="text-lg leading-none">+</span>
          Add Account
        </button>
        <button
          onClick={handleSubmitAll}
          disabled={cards.length === 0 || submitting}
          className="flex items-center gap-2 bg-slate-700 hover:bg-slate-600 disabled:opacity-40 disabled:cursor-not-allowed text-white text-sm font-medium px-4 py-2 rounded-lg border border-slate-600 transition-colors"
        >
          {submitting ? "Saving…" : "Save All"}
        </button>
      </div>

      {cards.length === 0 && (
        <div className="flex flex-1 items-center justify-center">
          <p className="text-slate-500 text-sm">
            No accounts yet. Click &quot;Add Account&quot; to get started.
          </p>
        </div>
      )}

      <div className="flex flex-col gap-4">
        {cards.map((card) => (
          <div key={card.id}>
            {errors[card.id] && (
              <p role="alert" className="text-sm text-red-400 mb-2">
                {errors[card.id]}
              </p>
            )}
            <AccountCard
              key={card.id}
              card={card}
              onChange={(updated) => updateCard(card.id, updated)}
              onDelete={() => deleteCard(card.id)}
            />
          </div>
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
