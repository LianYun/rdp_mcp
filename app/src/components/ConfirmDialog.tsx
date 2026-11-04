import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { useT } from "@/i18n";

interface Props {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  title: string;
  description?: string;
  confirmLabel?: string;
  destructive?: boolean;
  /** 额外选项（如“同时删除钥匙串条目”）。 */
  extra?: { label: string; defaultChecked?: boolean };
  onConfirm: (extra: boolean) => void | Promise<void>;
}

export function ConfirmDialog({ open, onOpenChange, title, description, confirmLabel, destructive, extra, onConfirm }: Props) {
  const t = useT();
  const [busy, setBusy] = useState(false);
  const [checked, setChecked] = useState(extra?.defaultChecked ?? false);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent data-testid="confirm-dialog">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description && <DialogDescription>{description}</DialogDescription>}
        </DialogHeader>
        {extra && (
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={checked} onChange={(e) => setChecked(e.target.checked)} />
            {extra.label}
          </label>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button
            variant={destructive ? "destructive" : "default"}
            disabled={busy}
            data-testid="confirm-ok"
            onClick={async () => {
              setBusy(true);
              try {
                await onConfirm(checked);
                onOpenChange(false);
              } finally {
                setBusy(false);
              }
            }}
          >
            {confirmLabel ?? t("common.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
