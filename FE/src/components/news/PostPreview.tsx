"use client";
import { useState } from "react";
import { parseInlines, parseMarkup } from "@/lib/news-flow";
import type { Inline } from "@/lib/news-flow";
import type { NewsLink } from "@/types/news";

function Inlines({ items }: { items: Inline[] }) {
  return (
    <>
      {items.map((it, i) => (
        <span key={i} className={`${it.bold ? "font-semibold text-white" : ""} ${it.italic ? "italic" : ""}`}>{it.text}</span>
      ))}
    </>
  );
}

// A Telegram-style card: what the channel will show. Text is rendered as React nodes, never as HTML.
export default function PostPreview(props: {
  title: string;
  emoji: string;
  text: string;
  hashtags: string;
  links: NewsLink[];
  imageURL: string;
  credit: string;
  creditURL: string;
  footer?: string;
}) {
  const [broken, setBroken] = useState("");
  const showImage = props.imageURL !== "" && broken !== props.imageURL;
  return (
    <div className="max-w-md overflow-hidden rounded-xl border border-slate-700 bg-slate-900 text-sm text-slate-200">
      {props.imageURL && (
        showImage ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={props.imageURL} alt="" referrerPolicy="no-referrer" className="max-h-64 w-full object-cover" onError={() => setBroken(props.imageURL)} />
        ) : (
          <p className="bg-slate-800 p-3 text-xs text-amber-300">Картинка не загрузилась в браузере. Telegram может показать её иначе, при необходимости замените.</p>
        )
      )}
      <div className="space-y-2 p-3">
        {props.title && <p className="font-semibold text-white">{props.emoji} {props.title}</p>}
        <div className="space-y-1 leading-relaxed">
          {parseMarkup(props.text).map((b, i) =>
            b.inlines.length === 0 ? (
              <div key={i} className="h-2" />
            ) : b.kind === "quote" ? (
              <blockquote key={i} className="whitespace-pre-wrap border-l-2 border-sky-400 pl-3 text-slate-300"><Inlines items={b.inlines} /></blockquote>
            ) : b.kind === "bullet" ? (
              <p key={i} className="pl-1">• <Inlines items={b.inlines} /></p>
            ) : (
              <p key={i}><Inlines items={b.inlines} /></p>
            ),
          )}
        </div>
        {props.hashtags && <p className="text-sky-300">{props.hashtags}</p>}
        {props.links.length > 0 && (
          <p className="text-xs text-slate-400">
            Источники: {props.links.map((l, i) => <span key={l.url}>{i > 0 && " · "}<a className="text-sky-300 underline" href={l.url} target="_blank" rel="noopener noreferrer">{l.title}</a></span>)}
          </p>
        )}
        {props.credit && (
          <p className="text-xs text-slate-400">
            Фото:{" "}
            {props.creditURL ? <a className="text-sky-300 underline" href={props.creditURL} target="_blank" rel="noopener noreferrer">{props.credit}</a> : props.credit} / Pexels
          </p>
        )}
        {props.footer && <p className="text-xs text-slate-300">{parseInlines(props.footer).map((x) => x.text).join("")}</p>}
      </div>
    </div>
  );
}
