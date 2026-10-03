import type { ClipEditor, VideoCardData, PublicationTarget } from "@/types";
export function validateTargets(
  targets: PublicationTarget[],
  requireStart = true,
) {
  if (!targets.length) return "Select at least one publication account";
  if (new Set(targets.map((t) => t.account_id)).size !== targets.length)
    return "An account can only appear once in a variant";
  for (const t of targets) {
    if (!Number.isInteger(t.account_id) || t.account_id <= 0)
      return "Invalid publication account";
    if (requireStart && !t.start_at)
      return "Choose a start time for each account";
    if (t.start_at && !Number.isFinite(Date.parse(t.start_at)))
      return "Invalid publication start time";
    if (!Number.isInteger(t.interval_seconds) || t.interval_seconds < 0)
      return "Publication interval must be zero or a positive whole number of seconds";
  }
  return null;
}
export function validateVideoCard(card: VideoCardData) {
  try {
    const url = new URL(card.youtubeUrl);
    if (
      !(
        url.hostname === "youtu.be" ||
        url.hostname === "youtube.com" ||
        url.hostname.endsWith(".youtube.com")
      ) ||
      url.protocol !== "https:"
    )
      return "Enter an HTTPS YouTube video URL";
  } catch {
    return "Enter a YouTube video URL";
  }
  if (
    card.sliceMode === "full" &&
    (!Number.isFinite(card.minVideoDuration) || card.minVideoDuration < 1)
  )
    return "Clip duration must be positive";
  if (card.addBackground && !card.backgroundName) return "Select a background";
  if (card.banners.length) {
    for (const b of card.banners) {
      if (!b.bannerName) return "Select a file for every banner";
      const error = validateTargets(b.targets ?? []);
      if (error) return error;
    }
    return null;
  }
  return validateTargets(card.targets ?? []);
}
export function validateEditor(draft: ClipEditor) {
  if (
    !Number.isFinite(draft.start_sec) ||
    !Number.isFinite(draft.end_sec) ||
    draft.start_sec < 0 ||
    draft.end_sec <= draft.start_sec
  )
    return "End must be later than start";
  if (draft.source_duration_sec && draft.end_sec > draft.source_duration_sec)
    return "Clip extends beyond the source video";
  if (!draft.title.trim() || draft.title.length > 100)
    return "Title must contain 1–100 characters";
  if (draft.description.length > 5000)
    return "Description must not exceed 5,000 characters";
  const ids = new Set<string>();
  for (const c of draft.cues) {
    if (ids.has(c.id)) return "Subtitle cue IDs must be unique";
    ids.add(c.id);
    if (
      !Number.isFinite(c.start) ||
      !Number.isFinite(c.end) ||
      c.start < 0 ||
      c.end <= c.start ||
      (draft.source_duration_sec && c.end > draft.source_duration_sec)
    )
      return "Each subtitle needs valid source timestamps";
  }
  return null;
}
