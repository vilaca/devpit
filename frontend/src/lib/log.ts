// slog-shaped console lines so retry/backoff is grep-able in DevTools.
// Failures and scheduled waits are warn; recovery is info.

const prefix = "devpit";

export function logWarn(
  msg: string,
  fields: Record<string, unknown> = {},
): void {
  console.warn(formatLog(msg, fields));
}

export function logInfo(
  msg: string,
  fields: Record<string, unknown> = {},
): void {
  console.info(formatLog(msg, fields));
}

export function formatLog(
  msg: string,
  fields: Record<string, unknown> = {},
): string {
  const parts = [`${prefix} ${msg}`];
  for (const [k, v] of Object.entries(fields)) {
    if (v === undefined || v === null || v === "") continue;
    parts.push(`${k}=${stringifyField(v)}`);
  }
  return parts.join(" ");
}

function stringifyField(v: unknown): string {
  if (typeof v === "string") return v;
  if (
    typeof v === "number" ||
    typeof v === "boolean" ||
    typeof v === "bigint"
  ) {
    return v.toString();
  }
  return JSON.stringify(v);
}
