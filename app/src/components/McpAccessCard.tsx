import { useEffect, useState } from "react";
import { Eye, EyeOff, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { CopyButton } from "@/components/CopyButton";
import { useT } from "@/i18n";
import { buildSnippet, type SnippetKind } from "@/lib/snippets";
import { useLive } from "@/stores/live";
import { toast } from "@/stores/toast";

/** MCP 地址、token（遮罩/复制/重新生成）与客户端接入片段。 */
export function McpAccessCard() {
  const t = useT();
  const client = useLive((s) => s.client);
  const running = useLive((s) => s.status === "running");
  const [token, setToken] = useState("");
  const [url, setUrl] = useState("");
  const [show, setShow] = useState(false);
  const [rotate, setRotate] = useState(false);

  useEffect(() => {
    if (!client || !running) return;
    let alive = true;
    Promise.all([client.mcpToken(), client.service()])
      .then(([tk, svc]) => {
        if (!alive) return;
        setToken(tk.mcp_token);
        setUrl(svc.mcp_url);
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [client, running]);

  const masked = token ? "•".repeat(Math.min(token.length, 24)) : t("mcp.no_token");
  const kinds: { key: SnippetKind; label: string }[] = [
    { key: "claude-code", label: "Claude Code" },
    { key: "json", label: "JSON" },
    { key: "claude-desktop", label: "Claude Desktop" },
  ];

  return (
    <Card data-testid="mcp-card">
      <CardHeader>
        <CardTitle>{t("mcp.title")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="space-y-1.5 text-sm">
          <div className="flex items-center gap-2">
            <span className="w-14 shrink-0 text-muted-foreground">URL</span>
            <code className="mono min-w-0 flex-1 truncate rounded bg-muted px-2 py-1" data-testid="mcp-url">
              {url || "-"}
            </code>
            <CopyButton text={url} size="icon" />
          </div>
          <div className="flex items-center gap-2">
            <span className="w-14 shrink-0 text-muted-foreground">Token</span>
            <code className="mono min-w-0 flex-1 truncate rounded bg-muted px-2 py-1" data-testid="mcp-token">
              {show ? token || t("mcp.no_token") : masked}
            </code>
            <Button variant="outline" size="icon" onClick={() => setShow(!show)} aria-label={t("mcp.toggle_token")} disabled={!token}>
              {show ? <EyeOff /> : <Eye />}
            </Button>
            <CopyButton text={token} size="icon" />
            <Button variant="outline" size="icon" onClick={() => setRotate(true)} aria-label={t("mcp.rotate")} disabled={!running} data-testid="mcp-rotate">
              <RefreshCw />
            </Button>
          </div>
        </div>
        <Tabs defaultValue="claude-code">
          <TabsList>
            {kinds.map((k) => (
              <TabsTrigger key={k.key} value={k.key}>
                {k.label}
              </TabsTrigger>
            ))}
          </TabsList>
          {kinds.map((k) => (
            <TabsContent key={k.key} value={k.key} className="space-y-2">
              <pre className="mono max-h-40 overflow-auto whitespace-pre-wrap break-all rounded bg-muted p-2 text-xs" data-testid={`snippet-${k.key}`}>
                {url ? buildSnippet(k.key, url, token, true) : "-"}
              </pre>
              <CopyButton text={url ? buildSnippet(k.key, url, token) : ""} label={t("mcp.copy_snippet")} />
            </TabsContent>
          ))}
        </Tabs>
      </CardContent>
      <ConfirmDialog
        open={rotate}
        onOpenChange={setRotate}
        title={t("mcp.rotate_title")}
        description={t("mcp.rotate_desc")}
        destructive
        onConfirm={async () => {
          try {
            const r = await client!.rotateMcpToken();
            setToken(r.mcp_token);
            setShow(true);
            toast.success(t("mcp.rotated"));
          } catch (e) {
            toast.error(e);
          }
        }}
      />
    </Card>
  );
}
