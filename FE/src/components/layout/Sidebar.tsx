"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useState } from "react";

const EXTERNAL_LINKS = [
  { label: "Grafana", href: "https://grafana.legendary-trio.me" },
  { label: "N8N", href: "https://n8n.legendary-trio.me" },
  { label: "MinIO", href: "https://minio.legendary-trio.me" },
  { label: "RabbitMQ", href: "https://rabbitmq.legendary-trio.me" },
  { label: "PgAdmin", href: "https://pgadmin.legendary-trio.me" },
  { label: "Portainer", href: "https://portainer.legendary-trio.me" },
];

function ChevronIcon({ open }: { open: boolean }) {
  return (
    <svg
      className={`w-4 h-4 transition-transform duration-200 ${open ? "rotate-90" : ""}`}
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
    </svg>
  );
}

export default function Sidebar() {
  const pathname = usePathname();
  const [adminOpen, setAdminOpen] = useState(true);
  const [servicesOpen, setServicesOpen] = useState(true);

  return (
    <aside className="w-60 bg-slate-800 border-r border-slate-700 flex flex-col shrink-0 overflow-y-auto">
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
              {[
                { href: "/admin/jobs", label: "View Jobs" },
                { href: "/admin/upload-video", label: "Upload Video" },
                { href: "/admin/accounts", label: "View Accounts" },
                { href: "/admin/create-account", label: "Create Account" },
                { href: "/admin/banners-list", label: "View Banners" },
                { href: "/admin/banners", label: "Create Banners" },
                { href: "/admin/backgrounds-list", label: "View Backgrounds" },
                { href: "/admin/backgrounds", label: "Create Backgrounds" },
              ].map(({ href, label }) => (
                <li key={href}>
                  <Link
                    href={href}
                    className={`flex items-center gap-2 px-6 py-2 text-sm rounded-md mx-2 transition-colors ${
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
                    <svg
                      className="w-3 h-3 ml-auto text-slate-500"
                      fill="none"
                      stroke="currentColor"
                      viewBox="0 0 24 24"
                    >
                      <path
                        strokeLinecap="round"
                        strokeLinejoin="round"
                        strokeWidth={2}
                        d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14"
                      />
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
