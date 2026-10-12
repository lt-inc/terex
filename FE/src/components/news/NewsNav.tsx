"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";

const LINKS = [
  ["/admin/news", "Потоки"],
  ["/admin/news/posts", "Посты"],
  ["/admin/news/sources", "Источники"],
  ["/admin/news/channels", "Telegram-каналы"],
] as const;

export default function NewsNav() {
  const path = usePathname();
  return (
    <nav aria-label="Новости" className="flex flex-wrap gap-2">
      {LINKS.map(([href, label]) => (
        <Link
          key={href}
          href={href}
          className={`rounded-lg border px-3 py-2 text-sm ${path === href ? "border-indigo-500 bg-indigo-600 text-white" : "border-slate-700 bg-slate-800 text-slate-300 hover:bg-slate-700"}`}
        >
          {label}
        </Link>
      ))}
    </nav>
  );
}
