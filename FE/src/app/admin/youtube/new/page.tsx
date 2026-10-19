"use client";
import { useRouter } from "next/navigation";
import { useState } from "react";
import FlowForm from "@/components/youtube/FlowForm";
import YouTubeNav from "@/components/youtube/YouTubeNav";
import { createYTFlow } from "@/lib/youtube";

export default function NewYouTubeFlowPage() {
  const router = useRouter();
  const [saving, setSaving] = useState(false);
  return (
    <div className="flex flex-col gap-5 p-4 sm:p-6">
      <h1 className="text-xl font-semibold">Новый YouTube-поток</h1>
      <YouTubeNav />
      <p className="text-sm text-slate-400">Поток создаётся выключенным: сначала добавьте источники, затем включите его.</p>
      <FlowForm
        saving={saving}
        onSave={async (draft) => {
          setSaving(true);
          try {
            const created = await createYTFlow({ ...draft, enabled: false });
            router.push(`/admin/youtube/${created.id}`);
          } finally {
            setSaving(false);
          }
        }}
      />
    </div>
  );
}
