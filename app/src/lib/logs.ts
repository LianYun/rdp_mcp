// 解析服务日志行（slog 文本格式：`time=… level=INFO msg="…" k=v`）。

export interface LogLine {
  raw: string;
  time?: string;
  level: "DEBUG" | "INFO" | "WARN" | "ERROR" | "";
  msg: string;
}

function field(line: string, key: string): string | undefined {
  const m = line.match(new RegExp(`(?:^|\\s)${key}=("(?:[^"\\\\]|\\\\.)*"|\\S*)`));
  if (!m) return undefined;
  const v = m[1];
  if (v.startsWith('"')) {
    try {
      return JSON.parse(v);
    } catch {
      return v.slice(1, -1);
    }
  }
  return v;
}

export function parseLogLine(raw: string): LogLine {
  const lv = (field(raw, "level") ?? "").toUpperCase();
  const level = lv === "DEBUG" || lv === "INFO" || lv === "WARN" || lv === "ERROR" ? lv : "";
  return { raw, time: field(raw, "time"), level, msg: field(raw, "msg") ?? raw };
}

export const LOG_LEVELS = ["debug", "info", "warn", "error"] as const;
