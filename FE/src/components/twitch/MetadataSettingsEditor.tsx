"use client";
import { useEffect, useState } from "react";
import { fetchImports, fetchTwitchFlowClips, previewTwitchMetadata } from "@/lib/api";
import { metadataVariables, parseMetadataHashtags, standardMetadataSettings, validateMetadataSettings } from "@/lib/twitch-metadata";
import type { TwitchFlow, TwitchMetadataPreview, TwitchMetadataSettings, TwitchSource } from "@/types/twitch";
import MetadataPreview from "./MetadataPreview";

const input = "w-full min-w-0 rounded-lg border border-slate-600 bg-slate-700 px-3 py-2 text-sm text-slate-100";
const button = "rounded-lg bg-slate-700 px-3 py-2 text-sm hover:bg-slate-600 disabled:opacity-40";
type Sample = { id: string; source: TwitchSource };

export default function MetadataSettingsEditor({ flow, onChange }: { flow: TwitchFlow; onChange: (value: TwitchMetadataSettings) => void }) {
  const settings = flow.metadata || standardMetadataSettings();
  const [tags, setTags] = useState((settings.custom_hashtags || []).map((tag) => `#${tag.replace(/^#/, "")}`).join(" "));
  const [samples, setSamples] = useState<Sample[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  const [shortSource, setShortSource] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [previews, setPreviews] = useState<{ fingerprint: string; short: TwitchMetadataPreview; compilation: TwitchMetadataPreview } | null>(null);
  const fingerprint = JSON.stringify({ settings, language: flow.language, sourceMode: flow.source_mode, category: flow.category_name, selected, shortSource });
  useEffect(() => {
    let active = true;
    setLoading(true);
    const load = flow.id
      ? fetchTwitchFlowClips(flow.id, { limit: "50" }).then((page) => page.items.map((clip) => ({ id: clip.import_id || clip.source.clip_id, source: clip.source })))
      : fetchImports().then((items) => (items || []).slice(0, 50).map((item) => ({ id: item.import_id, source: item.source })));
    load.then((items) => {
      if (!active) return;
      const unique = items.filter((item, index) => item.id && items.findIndex((other) => other.id === item.id) === index);
      setSamples(unique);
      setSelected(unique.slice(0, 3).map((item) => item.id));
      setShortSource(unique[0]?.id || "");
    }).catch((failure) => {
      if (active) setError(failure instanceof Error ? failure.message : String(failure));
    }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [flow.id]);
  const change = (patch: Partial<TwitchMetadataSettings>) => onChange({ ...settings, ...patch });
  async function preview() {
    const issue = validateMetadataSettings(settings);
    if (issue) { setError(issue); return; }
    setBusy(true);
    setError("");
    const context = {
      flow_id: flow.id || undefined,
      settings,
      source_mode: flow.source_mode,
      category_name: flow.category_name || "",
      language_filter: flow.language || "",
    };
    try {
      const [short, compilation] = await Promise.all([
        previewTwitchMetadata({ ...context, format: "short", title: samples.find((sample) => sample.id === shortSource)?.source.title || "Название Short", source_ids: shortSource ? [shortSource] : [] }),
        previewTwitchMetadata({ ...context, format: "compilation", title: "Лучшие моменты Twitch", source_ids: selected }),
      ]);
      setPreviews({ fingerprint, short, compilation });
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    } finally { setBusy(false); }
  }
  return (
    <section className="rounded-xl border border-slate-600 p-4 flex flex-col gap-4">
      <h2 className="font-semibold">Описание и хештеги</h2>
      <p className="text-sm text-slate-400">Настройки необязательны и действуют только на новые выпуски. Существующие описания и календарь не меняются. Шаблоны создаются без ИИ.</p>
      {([
        ["short_intro_template", "Вступление для Shorts", "Пустое поле: название выпуска и момент со стрима."],
        ["compilation_intro_template", "Вступление для подборок", "Пустое поле: название выпуска и число клипов."],
      ] as const).map(([field, label, hint]) => (
        <label key={field} className="flex flex-col gap-1 text-sm">
          {label}
          <textarea className={input} rows={3} value={settings[field] || ""} onChange={(event) => change({ [field]: event.target.value })} placeholder={hint} />
          <span className="text-xs text-slate-400">{hint} Ваш текст не переводится.</span>
        </label>
      ))}
      <p className="text-xs text-slate-400">Переменные: {metadataVariables.map((name) => `{${name}}`).join(", ")}. Стримеры, ссылки на их каналы, категория, контакт ceo@legendary-trio.me и хештеги добавляются отдельными блоками.</p>
      <label className="flex flex-col gap-1 text-sm">
        Хештеги потока
        <select className={input} value={settings.hashtag_mode || "standard"} onChange={(event) => change({ hashtag_mode: event.target.value as "standard" | "custom" })}>
          <option value="standard">Стандартные</option>
          <option value="custom">Собственные</option>
        </select>
      </label>
      {(settings.hashtag_mode || "standard") === "custom" ? (
        <label className="flex flex-col gap-1 text-sm">
          Собственные хештеги через пробел
          <input className={input} value={tags} placeholder="#юмор #реакция" onChange={(event) => { setTags(event.target.value); change({ custom_hashtags: parseMetadataHashtags(event.target.value) }); }} />
          <span className="text-xs text-slate-400">Заменяют общие и русские теги. #shorts, ник стримера для Short и известная категория сохраняются. Максимум 20 итоговых хештегов, повторы удаляются.</span>
        </label>
      ) : <p className="text-xs text-slate-400">#twitch #clips #highlights; для русских выпусков: #твич #моменты #приколы. Shorts: #shorts и ник стримера. Категория добавляется, если она задана и известна.</p>}
      <button type="button" className={`${button} self-start`} onClick={() => { onChange(standardMetadataSettings()); setTags(""); }}>Восстановить стандартные настройки</button>
      <div className="border-t border-slate-700 pt-3 flex flex-col gap-3">
        <h3 className="text-sm font-semibold">Предпросмотр на примере исходников</h3>
        <p className="text-xs text-slate-400">Загружаются только сведения о клипах; видео и GPU не используются. Выбор ниже нужен для примера и не меняет состав будущих выпусков.</p>
        {loading ? <p className="text-sm text-slate-400">Загружаем список клипов…</p> : samples.length ? <>
          <label className="flex flex-col gap-1 text-sm">Исходник для Short
            <select className={input} value={shortSource} onChange={(event) => setShortSource(event.target.value)}>
              {samples.map((sample) => <option key={sample.id} value={sample.id}>{sample.source.broadcaster_login} · {sample.source.title}</option>)}
            </select>
          </label>
          <details className="text-sm">
            <summary className="cursor-pointer">Пример состава подборки: {selected.length} клипов</summary>
            <div className="mt-2 max-h-44 overflow-y-auto flex flex-col gap-2">
              {samples.map((sample) => <label key={sample.id} className="flex items-start gap-2 text-xs text-slate-300"><input type="checkbox" checked={selected.includes(sample.id)} onChange={(event) => setSelected((current) => event.target.checked ? [...current, sample.id] : current.filter((id) => id !== sample.id))} /><span>{sample.source.broadcaster_login} · {sample.source.title}</span></label>)}
            </div>
          </details>
        </> : <p className="text-sm text-amber-300">Пока нет исходников для примера. Можно проверить шаблон; реальные участники и число клипов появятся после импорта.</p>}
        <button type="button" className={`${button} self-start`} disabled={busy || loading} onClick={() => void preview()}>{busy ? "Собираем описание…" : "Показать оба описания"}</button>
        {error && <p role="alert" className="text-sm text-red-400">{error}</p>}
        {previews && previews.fingerprint === fingerprint && <div className="grid gap-3 lg:grid-cols-2"><MetadataPreview result={previews.short} label="Short" /><MetadataPreview result={previews.compilation} label="Подборка" /></div>}
        <p className="text-xs text-slate-400">В описаниях Shorts обычные URL отображаются текстом: YouTube не делает их кликабельными. Хештеги помогают описать тему, но не гарантируют продвижение.</p>
      </div>
    </section>
  );
}
