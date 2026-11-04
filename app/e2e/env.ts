import path from "node:path";

// e2e 专用环境：只使用 /tmp/rdptest-ui、端口 13390（示例服务器）与 18766（remoter-mcp）。
export const ROOT = "/tmp/rdptest-ui";
export const STATE = path.join(ROOT, "state.json");
export const REPO = path.resolve(import.meta.dirname, "../..");
