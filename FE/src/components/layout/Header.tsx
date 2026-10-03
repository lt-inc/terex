"use client";
import { usePathname, useRouter } from "next/navigation";

const pages = [
  ["/admin/jobs", "Jobs"],
  ["/admin/upload-video", "Upload video"],
  ["/admin/calendar", "Calendar"],
  ["/admin/clips", "Clips"],
  ["/admin/accounts", "Accounts"],
  ["/admin/channels", "Channels"],
  ["/admin/banners-list", "Banners"],
  ["/admin/backgrounds-list", "Backgrounds"],
];

export default function Header() {
  const path = usePathname(),
    router = useRouter();
  const selected =
    pages.find(([url]) => path === url || path.startsWith(`${url}/`))?.[0] ??
    "";
  return (
    <header className="h-10 bg-slate-800 border-b border-slate-700 flex items-center px-4 shrink-0">
      <nav className="flex w-full items-center justify-between gap-4 md:hidden">
        <span className="font-semibold text-indigo-400">Terex</span>
        <select
          aria-label="Navigation"
          value={selected}
          className="rounded bg-slate-700 px-2 py-1 text-sm"
          onChange={(e) => router.push(e.target.value)}
        >
          <option value="" disabled>
            Menu
          </option>
          {pages.map(([url, label]) => (
            <option key={url} value={url}>
              {label}
            </option>
          ))}
        </select>
      </nav>
    </header>
  );
}
