const dayMs = 24 * 60 * 60 * 1000;

function beijingCalendarDay(value: Date): number | null {
  if (Number.isNaN(value.getTime())) return null;
  const shifted = new Date(value.getTime() + 8 * 60 * 60 * 1000);
  return Date.UTC(shifted.getUTCFullYear(), shifted.getUTCMonth(), shifted.getUTCDate());
}

function parseCalendarDay(value?: string): number | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value?.trim() ?? "");
  if (!match) return null;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const parsed = Date.UTC(year, month - 1, day);
  const check = new Date(parsed);
  if (
    check.getUTCFullYear() !== year ||
    check.getUTCMonth() !== month - 1 ||
    check.getUTCDate() !== day
  ) {
    return null;
  }
  return parsed;
}

export function teachingWeek(now: Date, termStart?: string): number {
  const current = beijingCalendarDay(now);
  const start = parseCalendarDay(termStart);
  if (current === null || start === null) return 0;
  const days = Math.floor((current - start) / dayMs);
  return days < 0 ? 0 : Math.floor(days / 7) + 1;
}

export function weekMatches(week: number, weeks?: string): boolean {
  const normalized = weeks?.trim() ?? "";
  if (!normalized) return true;
  if (week < 1) return false;
  return normalized.split(",").some((part) => {
    const bounds = part.trim().split("-");
    const first = Number(bounds[0]);
    const last = bounds.length > 1 ? Number(bounds[1]) : first;
    return Number.isInteger(first) && Number.isInteger(last) && week >= first && week <= last;
  });
}

export function weekLabel(week: number): string {
  return week > 0 ? `第 ${week} 周` : "";
}
