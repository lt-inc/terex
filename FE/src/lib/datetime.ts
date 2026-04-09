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
