"use client";

import { useEffect, useState, useCallback } from "react";
import AccountRecordCard from "@/components/records/AccountRecordCard";
import { fetchAccountsList, updateAccount, deleteAccount } from "@/lib/api";
import type { AccountRecord } from "@/types";

export default function AccountsPage() {
  const [records, setRecords] = useState<AccountRecord[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try { setRecords(await fetchAccountsList()); }
    finally { setLoading(false); }
  }, []);

  useEffect(() => { load(); }, [load]);

  return (
    <div className="p-6 flex flex-col gap-6 min-h-full">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-slate-100">Accounts</h1>
        <button onClick={load} disabled={loading}
          className="bg-slate-700 hover:bg-slate-600 disabled:opacity-40 text-white text-sm font-medium px-4 py-2 rounded-lg border border-slate-600 transition-colors">
          {loading ? "Loading…" : "Refresh"}
        </button>
      </div>

      {!loading && records.length === 0 && (
        <div className="flex flex-1 items-center justify-center">
          <p className="text-slate-500 text-sm">No accounts yet.</p>
        </div>
      )}

      <div className="flex flex-col gap-3">
        {records.map((r) => (
          <AccountRecordCard
            key={r.id}
            record={r}
            onUpdated={(updated) => setRecords((prev) => prev.map((x) => x.id === updated.id ? updated : x))}
            onDeleted={() => setRecords((prev) => prev.filter((x) => x.id !== r.id))}
            onSave={(id, data) => updateAccount(id, data)}
            onDelete={(id) => deleteAccount(id)}
          />
        ))}
      </div>
    </div>
  );
}
