import type { TwitchMetadataPreview } from "@/types/twitch";

export default function MetadataPreview({ result, label }: { result: TwitchMetadataPreview; label: string }) {
  return (
    <section className="rounded-lg border border-slate-600 bg-slate-900 p-3 flex flex-col gap-2">
      <h3 className="text-sm font-medium">{label}</h3>
      <p className="text-xs text-slate-400">
        Язык стандартного текста: {result.language === "ru" ? "русский" : "английский"} · Описание {result.description_bytes}/5000 байт · Теги {result.tag_characters}/500 символов · Хештеги {result.hashtags?.length || 0}/20
      </p>
      <div className="whitespace-pre-wrap break-words text-sm text-slate-200">{result.description}</div>
      {!!result.errors?.length && (
        <ul role="alert" className="list-disc pl-4 text-sm text-amber-300">
          {result.errors.map((error, index) => <li key={index}>{error}</li>)}
        </ul>
      )}
    </section>
  );
}
