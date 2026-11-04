// .rdp 文件导入：解析 `full address`、`username`、`domain` 等常用字段。

export interface RdpImport {
  host?: string;
  port?: number;
  username?: string;
  domain?: string;
  width?: number;
  height?: number;
  security?: "auto" | "nla" | "tls" | "rdp";
}

/** 解析 .rdp 文本，每行形如 `key:type:value`。 */
export function parseRdpFile(text: string): RdpImport {
  const kv = new Map<string, string>();
  for (const raw of text.replace(/^﻿/, "").split(/\r?\n/)) {
    const line = raw.trim();
    if (!line) continue;
    const m = line.match(/^([^:]+):([sib]):(.*)$/i);
    if (!m) continue;
    kv.set(m[1].trim().toLowerCase(), m[3].trim());
  }
  const out: RdpImport = {};

  const addr = kv.get("full address") ?? kv.get("alternate full address");
  if (addr) {
    // 支持 host、host:port、[ipv6]:port
    const v6 = addr.match(/^\[([^\]]+)\](?::(\d+))?$/);
    if (v6) {
      out.host = v6[1];
      if (v6[2]) out.port = Number(v6[2]);
    } else {
      const i = addr.lastIndexOf(":");
      if (i > 0 && /^\d+$/.test(addr.slice(i + 1)) && addr.indexOf(":") === i) {
        out.host = addr.slice(0, i);
        out.port = Number(addr.slice(i + 1));
      } else {
        out.host = addr;
      }
    }
  }
  const p = kv.get("server port");
  if (p && /^\d+$/.test(p) && out.port === undefined) out.port = Number(p);

  const user = kv.get("username");
  if (user) {
    // DOMAIN\user 或 user@domain.com
    const bs = user.indexOf("\\");
    if (bs > 0) {
      out.domain = user.slice(0, bs);
      out.username = user.slice(bs + 1);
    } else {
      out.username = user;
    }
  }
  const dom = kv.get("domain");
  if (dom && !out.domain) out.domain = dom;

  const w = kv.get("desktopwidth");
  const h = kv.get("desktopheight");
  if (w && /^\d+$/.test(w)) out.width = Number(w);
  if (h && /^\d+$/.test(h)) out.height = Number(h);

  const nla = kv.get("enablecredsspsupport");
  if (nla === "0") out.security = "tls";
  else if (nla === "1") out.security = "nla";
  return out;
}

/** 由文件名推断机器名（只保留 [A-Za-z0-9_-]）。 */
export function machineNameFromFile(fileName: string, host?: string): string {
  const base = fileName.replace(/\.rdp$/i, "");
  const clean = (s: string) => s.replace(/[^A-Za-z0-9_-]+/g, "-").replace(/^-+|-+$/g, "");
  return clean(base) || clean(host ?? "") || "machine";
}
