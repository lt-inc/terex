"use client";
import { useRouter } from "next/navigation";
import { useState } from "react";
import FlowEditor from "@/components/news/FlowEditor";
import NewsNav from "@/components/news/NewsNav";
import { createNewsFlow } from "@/lib/news";

export default function NewNewsFlowPage() {
  const router = useRouter();
  const [saving, setSaving] = useState(false);
  return (
    <div className="flex flex-col gap-5 p-4 sm:p-6">
      <h1 className="text-xl font-semibold">Новый новостной поток</h1>
      <NewsNav />
      <FlowEditor
        saving={saving}
        onSave={async (flow) => {
          setSaving(true);
          try {
            const created = await createNewsFlow(flow);
            router.push(`/admin/news/${created.id}`);
          } finally {
            setSaving(false);
          }
        }}
      />
    </div>
  );
}
