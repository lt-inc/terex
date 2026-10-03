export const DEFAULT_TIMEZONE = "Asia/Tomsk";
export const TIMEZONES = [
  "Asia/Tomsk",
  "Europe/Moscow",
  "UTC",
  "Europe/Berlin",
  "America/New_York",
  "America/Los_Angeles",
];
export function zonedParts(
  iso: string,
  zone = DEFAULT_TIMEZONE,
): { date: string; time: string } {
  const parts = new Intl.DateTimeFormat("en-CA", {
    timeZone: zone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).formatToParts(new Date(iso));
  const p = Object.fromEntries(parts.map((x) => [x.type, x.value]));
  return {
    date: `${p.year}-${p.month}-${p.day}`,
    time: `${p.hour}:${p.minute}`,
  };
}
export function formatInZone(iso: string, zone = DEFAULT_TIMEZONE) {
  return new Intl.DateTimeFormat("ru-RU", {
    timeZone: zone,
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).format(new Date(iso));
}
export function zonePartsToISO(
  date: string,
  time: string,
  zone = DEFAULT_TIMEZONE,
): string {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date) || !/^\d{2}:\d{2}$/.test(time))
    throw new Error("Choose a complete date and time");
  const desired = Date.parse(`${date}T${time}:00Z`);
  if (!Number.isFinite(desired)) throw new Error("Invalid date or time");
  let instant = desired;
  for (let i = 0; i < 4; i++) {
    const shown = zonedParts(new Date(instant).toISOString(), zone);
    const asUTC = Date.parse(`${shown.date}T${shown.time}:00Z`);
    const delta = desired - asUTC;
    if (!delta) break;
    instant += delta;
  }
  const result = new Date(instant).toISOString();
  const parts = zonedParts(result, zone);
  if (parts.date !== date || parts.time !== time)
    throw new Error("This local time does not exist in the selected timezone");
  return result;
}
export const localInputToISO = (value: string) => {
  const [d, t] = value.split("T");
  return zonePartsToISO(d, t);
};
export const localPartsToISO = (date: string, time: string) =>
  zonePartsToISO(date, time);
export const isoToUTC7Parts = (iso: string) => zonedParts(iso);
export const utc7PartsToISO = (date: string, time: string) =>
  zonePartsToISO(date, time);
export function addDays(date: string, count: number) {
  const d = new Date(`${date}T12:00:00Z`);
  d.setUTCDate(d.getUTCDate() + count);
  return d.toISOString().slice(0, 10);
}
export function shiftMonth(date: string, count: number) {
  const d = new Date(`${date}T12:00:00Z`);
  d.setUTCDate(1);
  d.setUTCMonth(d.getUTCMonth() + count);
  return d.toISOString().slice(0, 10);
}
export function calendarDays(date: string, view: "week" | "month") {
  const current = new Date(`${date}T12:00:00Z`);
  if (view === "month") current.setUTCDate(1);
  const weekday = (current.getUTCDay() + 6) % 7;
  current.setUTCDate(current.getUTCDate() - weekday);
  return Array.from({ length: view === "week" ? 7 : 42 }, (_, i) =>
    addDays(current.toISOString().slice(0, 10), i),
  );
}
