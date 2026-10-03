"use client";
import { useEffect, useId, useRef } from "react";

export default function ConfirmAction({
  title,
  message,
  confirmLabel = "Confirm",
  onConfirm,
  onCancel,
}: {
  title: string;
  message: string;
  confirmLabel?: string;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const titleID = useId();
  const ref = useRef<HTMLElement>(null);
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    ref.current?.querySelector<HTMLButtonElement>("button")?.focus();
    return () => previous?.focus();
  }, []);
  return (
    <div
      className="fixed inset-0 z-[60] bg-black/75 flex items-center justify-center p-4"
      onClick={(e) => {
        e.stopPropagation();
        if (e.target === e.currentTarget) onCancel();
      }}
    >
      <section
        ref={ref}
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={titleID}
        className="w-full max-w-md rounded-xl border border-slate-600 bg-slate-800 p-5"
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            e.stopPropagation();
            onCancel();
          }
          if (e.key === "Tab") {
            const buttons =
              ref.current?.querySelectorAll<HTMLButtonElement>("button");
            if (!buttons?.length) return;
            const first = buttons[0],
              last = buttons[buttons.length - 1];
            if (e.shiftKey && document.activeElement === first) {
              e.preventDefault();
              last.focus();
            } else if (!e.shiftKey && document.activeElement === last) {
              e.preventDefault();
              first.focus();
            }
          }
        }}
      >
        <h2 id={titleID} className="font-semibold">
          {title}
        </h2>
        <p className="mt-3 text-sm text-slate-300">{message}</p>
        <div className="mt-5 flex justify-end gap-2">
          <button
            className="rounded-lg bg-slate-700 px-4 py-2 text-sm"
            onClick={onCancel}
          >
            Back
          </button>
          <button
            className="rounded-lg bg-indigo-600 px-4 py-2 text-sm"
            onClick={onConfirm}
          >
            {confirmLabel}
          </button>
        </div>
      </section>
    </div>
  );
}
