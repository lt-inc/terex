"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useState, useEffect } from "react";

const EXTERNAL_LINKS = [
  { label: "Grafana",   href: "https://grafana.legendary-trio.me" },
  { label: "N8N",       href: "https://n8n.legendary-trio.me" },
  { label: "MinIO",     href: "https://minio.legendary-trio.me" },
  { label: "RabbitMQ",  href: "https://rabbitmq.legendary-trio.me" },
  { label: "PgAdmin",   href: "https://pgadmin.legendary-trio.me" },
  { label: "Portainer", href: "https://portainer.legendary-trio.me" },
  { label: "Vault",     href: "https://vault.legendary-trio.me" },
];

const ADMIN_GROUPS = [
  {
    key: "jobs",
    label: "Jobs",
    links: [
      { href: "/admin/jobs",         label: "View" },
      { href: "/admin/upload-video", label: "Upload" },
    ],
  },
  {
    key: "accounts",
    label: "Accounts",
    links: [
      { href: "/admin/accounts",       label: "View" },
      { href: "/admin/create-account", label: "Create" },
    ],
  },
  {
    key: "banners",
    label: "Banners",
    links: [
      { href: "/admin/banners-list", label: "View" },
      { href: "/admin/banners",      label: "Create" },
    ],
  },
  {
    key: "backgrounds",
    label: "Backgrounds",
    links: [
      { href: "/admin/backgrounds-list", label: "View" },
      { href: "/admin/backgrounds",      label: "Create" },
    ],
  },
  {
    key: "channels",
    label: "Channels",
    links: [
      { href: "/admin/channels", label: "Monitoring" },
    ],
  },
  {
    key: "clips",
    label: "Clips",
    links: [
      { href: "/admin/clips", label: "Approval" },
    ],
  },
];

function ChevronIcon({ open }: { open: boolean }) {
  return (
    <svg
      className={`w-3.5 h-3.5 transition-transform duration-200 ${open ? "rotate-90" : ""}`}
      fill="none" stroke="currentColor" viewBox="0 0 24 24"
    >
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
    </svg>
  );
}

export default function Sidebar() {
  const pathname = usePathname();
  const [adminOpen, setAdminOpen] = useState(true);
  const [servicesOpen, setServicesOpen] = useState(true);

  // Each subgroup open state — default open if current path is in that group
  const initialGroupOpen = () =>
    Object.fromEntries(
      ADMIN_GROUPS.map((g) => [
        g.key,
        g.links.some((l) => pathname === l.href),
      ])
    );

  const [groupOpen, setGroupOpen] = useState<Record<string, boolean>>(initialGroupOpen);

  // Sync when pathname changes (e.g. navigation)
  useEffect(() => {
    setGroupOpen((prev) => {
      const next = { ...prev };
      for (const g of ADMIN_GROUPS) {
        if (g.links.some((l) => pathname === l.href)) {
          next[g.key] = true;
        }
      }
      return next;
    });
  }, [pathname]);

  function toggleGroup(key: string) {
    setGroupOpen((prev) => ({ ...prev, [key]: !prev[key] }));
  }

  return (
    <aside className="w-56 bg-slate-800 border-r border-slate-700 flex flex-col shrink-0 overflow-y-auto">
      <div className="px-4 py-5 border-b border-slate-700">
        <span className="text-indigo-400 font-bold text-lg tracking-wide">Terex</span>
      </div>

      <nav className="flex-1 py-3">
        {/* Admin group */}
        <div>
          <button
            onClick={() => setAdminOpen((o) => !o)}
            className="w-full flex items-center gap-2 px-4 py-2 text-xs font-semibold uppercase tracking-widest text-slate-400 hover:text-slate-200 transition-colors"
          >
            <ChevronIcon open={adminOpen} />
            Admin
          </button>

          {adminOpen && (
            <ul className="mt-1">
              {ADMIN_GROUPS.map((group) => {
                const isOpen = groupOpen[group.key] ?? false;
                const isGroupActive = group.links.some((l) => pathname === l.href);
                return (
                  <li key={group.key}>
                    {/* Sub-group header */}
                    <button
                      onClick={() => toggleGroup(group.key)}
                      className={`w-full flex items-center gap-2 px-5 py-1.5 text-xs font-medium transition-colors ${
                        isGroupActive ? "text-indigo-300" : "text-slate-400 hover:text-slate-200"
                      }`}
                    >
                      <ChevronIcon open={isOpen} />
                      {group.label}
                    </button>

                    {/* Sub-group links */}
                    {isOpen && (
                      <ul>
                        {group.links.map(({ href, label }) => (
                          <li key={href}>
                            <Link
                              href={href}
                              className={`flex items-center gap-2 px-8 py-1.5 text-sm rounded-md mx-2 transition-colors ${
                                pathname === href
                                  ? "bg-indigo-600 text-white"
                                  : "text-slate-300 hover:bg-slate-700 hover:text-white"
                              }`}
                            >
                              {label}
                            </Link>
                          </li>
                        ))}
                      </ul>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </div>

        {/* Other Services group */}
        <div className="mt-4">
          <button
            onClick={() => setServicesOpen((o) => !o)}
            className="w-full flex items-center gap-2 px-4 py-2 text-xs font-semibold uppercase tracking-widest text-slate-400 hover:text-slate-200 transition-colors"
          >
            <ChevronIcon open={servicesOpen} />
            Other Services
          </button>

          {servicesOpen && (
            <ul className="mt-1">
              {EXTERNAL_LINKS.map((link) => (
                <li key={link.href}>
                  <a
                    href={link.href}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="flex items-center gap-2 px-6 py-2 text-sm text-slate-300 hover:bg-slate-700 hover:text-white rounded-md mx-2 transition-colors"
                  >
                    {link.label}
                    <svg className="w-3 h-3 ml-auto text-slate-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
                    </svg>
                  </a>
                </li>
              ))}
            </ul>
          )}
        </div>
      </nav>
    </aside>
  );
}
