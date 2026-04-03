"use client";

import { useRef, useState } from "react";

interface FileDropzoneProps {
  file: File | null;
  onChange: (file: File | null) => void;
}

export default function FileDropzone({ file, onChange }: FileDropzoneProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = useState(false);

  function handleDrop(e: React.DragEvent) {
    e.preventDefault();
    setDragging(false);
    const dropped = e.dataTransfer.files[0];
    if (dropped) onChange(dropped);
  }

  function handleChange(e: React.ChangeEvent<HTMLInputElement>) {
    const picked = e.target.files?.[0] ?? null;
    onChange(picked);
    e.target.value = "";
  }

  return (
    <div
      onDragOver={(e) => { e.preventDefault(); setDragging(true); }}
      onDragLeave={() => setDragging(false)}
      onDrop={handleDrop}
      onClick={() => inputRef.current?.click()}
      className={`flex flex-col items-center justify-center gap-1 rounded-md border-2 border-dashed px-4 py-5 cursor-pointer transition-colors text-sm ${
        dragging
          ? "border-indigo-500 bg-indigo-950/30 text-indigo-300"
          : "border-slate-600 bg-slate-700/40 text-slate-400 hover:border-slate-500 hover:text-slate-300"
      }`}
    >
      <input ref={inputRef} type="file" className="hidden" onChange={handleChange} />
      {file ? (
        <span className="text-slate-200 truncate max-w-full">{file.name}</span>
      ) : (
        <>
          <span>Drop file here or <span className="text-indigo-400">choose</span></span>
        </>
      )}
    </div>
  );
}
