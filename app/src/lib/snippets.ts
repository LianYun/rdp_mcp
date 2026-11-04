// MCP 客户端接入片段（docs/ui.md §4.1）。

export type SnippetKind = "claude-code" | "json" | "claude-desktop";

const MASK = "<token>";

function authHeader(token: string, mask: boolean): string {
  return `Bearer ${mask ? MASK : token}`;
}

/** 生成接入片段；`mask` 为 true 时 token 用占位符替代（用于屏幕展示）。 */
export function buildSnippet(kind: SnippetKind, url: string, token: string, mask = false): string {
  const hasToken = token !== "";
  switch (kind) {
    case "claude-code": {
      const base = `claude mcp add --transport http rdp ${url}`;
      return hasToken ? `${base} --header "Authorization: ${authHeader(token, mask)}"` : base;
    }
    case "json": {
      const server: Record<string, unknown> = { type: "http", url };
      if (hasToken) server.headers = { Authorization: authHeader(token, mask) };
      return JSON.stringify({ mcpServers: { rdp: server } }, null, 2);
    }
    case "claude-desktop": {
      const args = ["-y", "mcp-remote", url];
      if (hasToken) args.push("--header", `Authorization: ${authHeader(token, mask)}`);
      if (url.startsWith("http://")) args.push("--allow-http");
      return JSON.stringify({ mcpServers: { rdp: { command: "npx", args } } }, null, 2);
    }
  }
}
