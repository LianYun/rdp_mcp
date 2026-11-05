// MCP 客户端接入片段（docs/ui.md §4.1）。

export type SnippetKind = "claude-code" | "codex-cli" | "codex-app" | "json" | "claude-desktop";

/** 客户端里注册的服务名。 */
export const SERVER_NAME = "remoter";
/** Codex CLI 读取 token 的环境变量（`codex mcp add` 不能直接写入请求头）。 */
export const CODEX_TOKEN_ENV = "REMOTER_MCP_TOKEN";

const MASK = "<token>";

function authHeader(token: string, mask: boolean): string {
  return `Bearer ${mask ? MASK : token}`;
}

/** TOML 基本字符串（转义反斜杠与双引号）。 */
function tomlString(s: string): string {
  return `"${s.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
}

/** 生成接入片段；`mask` 为 true 时 token 用占位符替代（用于屏幕展示）。 */
export function buildSnippet(kind: SnippetKind, url: string, token: string, mask = false): string {
  const hasToken = token !== "";
  switch (kind) {
    case "claude-code": {
      const base = `claude mcp add --transport http ${SERVER_NAME} ${url}`;
      return hasToken ? `${base} --header "Authorization: ${authHeader(token, mask)}"` : base;
    }
    case "codex-cli": {
      const base = `codex mcp add ${SERVER_NAME} --url ${url}`;
      if (!hasToken) return base;
      // Codex 在启动时从环境变量读取 token，因此该变量需写入 shell 配置（如 ~/.zshrc）。
      return [
        `export ${CODEX_TOKEN_ENV}=${mask ? MASK : token}`,
        `${base} --bearer-token-env-var ${CODEX_TOKEN_ENV}`,
      ].join("\n");
    }
    case "codex-app": {
      // ~/.codex/config.toml，Codex app 与 Codex CLI 共用。截图、run_command 可能超过默认 60 秒。
      const lines = [`[mcp_servers.${SERVER_NAME}]`, `url = ${tomlString(url)}`];
      if (hasToken) lines.push(`http_headers = { "Authorization" = ${tomlString(authHeader(token, mask))} }`);
      lines.push("startup_timeout_sec = 30", "tool_timeout_sec = 180");
      return lines.join("\n");
    }
    case "json": {
      const server: Record<string, unknown> = { type: "http", url };
      if (hasToken) server.headers = { Authorization: authHeader(token, mask) };
      return JSON.stringify({ mcpServers: { [SERVER_NAME]: server } }, null, 2);
    }
    case "claude-desktop": {
      const args = ["-y", "mcp-remote", url];
      if (hasToken) args.push("--header", `Authorization: ${authHeader(token, mask)}`);
      if (url.startsWith("http://")) args.push("--allow-http");
      return JSON.stringify({ mcpServers: { [SERVER_NAME]: { command: "npx", args } } }, null, 2);
    }
  }
}

/** 片段下方的使用说明（i18n key），没有说明时返回 undefined。 */
export function snippetHintKey(kind: SnippetKind, hasToken: boolean): string | undefined {
  switch (kind) {
    case "codex-cli":
      return hasToken ? "mcp.hint.codex_cli_token" : "mcp.hint.codex_cli";
    case "codex-app":
      return "mcp.hint.codex_app";
    case "claude-desktop":
      return "mcp.hint.claude_desktop";
  }
  return undefined;
}
