interface JobSource {
  source_kind?: string;
  source_url?: string;
  source_id?: string;
  youtube_url?: string;
  slice_mode?: string;
}

export function jobSourceLabel(job: JobSource): string {
  if (job.source_kind === "compilation" || job.slice_mode === "compilation") return "Compilation";
  if (job.source_kind === "twitch" || job.source_kind === "twitch_clip") return "Twitch clip";
  return "YouTube";
}

export function jobSourceURL(job: JobSource): string {
  return job.source_url || job.youtube_url || "";
}

export function montageLabel(mode: string): string {
  if (mode === "highlights") return "Highlights";
  if (mode === "full_clip") return "Whole clip";
  if (mode === "compilation") return "Compilation";
  return "Full video by intervals";
}
