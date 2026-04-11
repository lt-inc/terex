/**
 * Convert a datetime-local input value ("YYYY-MM-DDTHH:mm") to UTC ISO string.
 * Uses the multi-argument Date constructor which always treats inputs as LOCAL time,
 * unlike new Date(string) which is implementation-defined for strings without timezone.
 */
export function localInputToISO(localStr: string): string {
  const [datePart, timePart] = localStr.split("T");
  const [y, mo, d] = datePart.split("-").map(Number);
  const [h, m] = timePart.split(":").map(Number);
  return new Date(y, mo - 1, d, h, m).toISOString();
}

/**
 * Convert separate date ("YYYY-MM-DD") and time ("HH:mm") local inputs to UTC ISO string.
 */
export function localPartsToISO(date: string, time: string): string {
  const [y, mo, d] = date.split("-").map(Number);
  const [h, m] = time.split(":").map(Number);
  return new Date(y, mo - 1, d, h, m).toISOString();
}

const UTC7_OFFSET_MS = 7 * 60 * 60 * 1000;

/**
 * Convert UTC ISO string to UTC+7 date ("YYYY-MM-DD") and time ("HH:mm") for form inputs.
 */
export function isoToUTC7Parts(iso: string): { date: string; time: string } {
  const shifted = new Date(new Date(iso).getTime() + UTC7_OFFSET_MS);
  const pad = (n: number) => String(n).padStart(2, "0");
  const date = `${shifted.getUTCFullYear()}-${pad(shifted.getUTCMonth() + 1)}-${pad(shifted.getUTCDate())}`;
  const time = `${pad(shifted.getUTCHours())}:${pad(shifted.getUTCMinutes())}`;
  return { date, time };
}

/**
 * Convert UTC+7 date ("YYYY-MM-DD") and time ("HH:mm") inputs back to UTC ISO string.
 */
export function utc7PartsToISO(date: string, time: string): string {
  const [y, mo, d] = date.split("-").map(Number);
  const [h, m] = time.split(":").map(Number);
  const utcMs = Date.UTC(y, mo - 1, d, h, m) - UTC7_OFFSET_MS;
  return new Date(utcMs).toISOString();
}
