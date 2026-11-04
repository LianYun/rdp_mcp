import { useState } from "react";
import { ShieldAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { useT } from "@/i18n";
import { useLive } from "@/stores/live";
import { toast } from "@/stores/toast";

/** 证书确认（cert_verify 事件 / pending_cert）：仅本次 / 始终信任 / 拒绝。一次只展示一台机器的待确认证书。 */
export function CertDialog() {
  const t = useT();
  const certs = useLive((s) => s.certs);
  const client = useLive((s) => s.client);
  const [busy, setBusy] = useState(false);
  const cert = Object.values(certs)[0];
  if (!cert) return null;

  const answer = async (accept: "once" | "always" | "reject") => {
    if (!client) return;
    setBusy(true);
    try {
      await client.answerCert(cert.machine, accept);
    } catch (e) {
      toast.error(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={() => {}}>
      <DialogContent hideClose data-testid="cert-dialog" onInteractOutside={(e) => e.preventDefault()} onEscapeKeyDown={(e) => e.preventDefault()}>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <ShieldAlert className="size-5 text-warning" />
            {t("cert.title", { machine: cert.machine })}
          </DialogTitle>
          <DialogDescription>{cert.reason || t("cert.desc")}</DialogDescription>
        </DialogHeader>
        <dl className="grid grid-cols-[88px_1fr] gap-x-3 gap-y-1.5 text-sm">
          <dt className="text-muted-foreground">{t("cert.host")}</dt>
          <dd className="mono">
            {cert.host}:{cert.port}
          </dd>
          <dt className="text-muted-foreground">{t("cert.fingerprint")}</dt>
          <dd className="mono break-all" data-testid="cert-fingerprint">
            {cert.fingerprint}
          </dd>
          {cert.subject && (
            <>
              <dt className="text-muted-foreground">{t("cert.subject")}</dt>
              <dd className="break-all">{cert.subject}</dd>
            </>
          )}
          {cert.issuer && (
            <>
              <dt className="text-muted-foreground">{t("cert.issuer")}</dt>
              <dd className="break-all">{cert.issuer}</dd>
            </>
          )}
        </dl>
        <p className="text-xs text-muted-foreground">{t("cert.timeout")}</p>
        <DialogFooter>
          <Button variant="destructive" disabled={busy} onClick={() => answer("reject")} data-testid="cert-reject">
            {t("cert.reject")}
          </Button>
          <Button variant="outline" disabled={busy} onClick={() => answer("once")} data-testid="cert-once">
            {t("cert.once")}
          </Button>
          <Button disabled={busy} onClick={() => answer("always")} data-testid="cert-always">
            {t("cert.always")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
