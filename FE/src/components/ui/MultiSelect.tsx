"use client";

import { useState, useRef, useEffect } from "react";

interface MultiSelectProps {
  label: string;
  options: string[];
  selected: string[];
  onChange: (selected: string[]) => void;
}

export default function MultiSelect({ label, options, selected, onChange }: MultiSelectProps) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  function toggle(option: string) {
    if (selected.includes(option)) {
      onChange(selected.filter((s) => s !== option));
    } else {
      onChange([...selected, option]);
    }
  }

  return (
    <div className="flex flex-col gap-1" ref={ref}>
      <span className="text-xs text-slate-400">{label}</span>
      <div className="relative">
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          className="w-full text-left bg-slate-700 border border-slate-600 rounded-md px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-2 focus:ring-indigo-500"
        >
          {selected.length === 0 ? (
            <span className="text-slate-400">Select accounts…</span>
          ) : (
            <span className="truncate">{selected.join(", ")}</span>
          )}
          <span className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-400">▾</span>
        </button>

        {open && (
          <div className="absolute z-50 mt-1 w-full bg-slate-700 border border-slate-600 rounded-md shadow-lg max-h-48 overflow-auto">
            {options.map((opt) => (
              <label
                key={opt}
                className="flex items-center gap-2 px-3 py-2 cursor-pointer hover:bg-slate-600 text-sm text-slate-100"
              >
                <input
                  type="checkbox"
                  className="accent-indigo-500"
                  checked={selected.includes(opt)}
                  onChange={() => toggle(opt)}
                />
                {opt}
              </label>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
