"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
export default function TwitchNav() {
  const path = usePathname();
  return (
    <nav aria-label="Twitch" className="flex flex-wrap gap-2">
      {[
        ["/admin/twitch", "Потоки"],
        ["/admin/twitch/clips", "Клипы"],
        ["/admin/compilations", "Подборки"],
        ["/admin/twitch/storage", "Хранение и очистка"],
        ["/admin/twitch/settings", "Настройки сервиса"],
      ].map(([href, label]) => (
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
