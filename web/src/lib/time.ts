export const BEIJING_TIME_ZONE = "Asia/Shanghai";

const dateTime = new Intl.DateTimeFormat("zh-CN", {
  timeZone: BEIJING_TIME_ZONE,
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hour12: false
});

const dateOnly = new Intl.DateTimeFormat("zh-CN", {
  timeZone: BEIJING_TIME_ZONE,
  year: "numeric",
  month: "2-digit",
  day: "2-digit"
});

function asDate(value: string | number | Date): Date | null {
  const parsed = value instanceof Date ? value : new Date(value);
  return Number.isNaN(parsed.getTime()) ? null : parsed;
}

export function formatBeijing(value?: string | number | Date | null): string {
  if (value === undefined || value === null || value === "") return "—";
  const parsed = asDate(value);
  return parsed ? dateTime.format(parsed) : String(value);
}

export function formatBeijingDate(value?: string | number | Date | null): string {
  if (value === undefined || value === null || value === "") return "—";
  const parsed = asDate(value);
  return parsed ? dateOnly.format(parsed) : String(value);
}

export function beijingDateKey(value?: string | number | Date | null): string {
  const formatted = formatBeijingDate(value);
  return formatted === "—" ? formatted : formatted.replaceAll("/", "-");
}
