import { useState } from "react";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, Input, Select, Textarea } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { useT } from "@/i18n";
import { toMachineDef, validateForm, type MachineForm } from "@/lib/machineForm";
import { useLive } from "@/stores/live";
import { toast } from "@/stores/toast";
import type { TestResult } from "@/api/types";

interface Props {
  initial: MachineForm;
  /** 编辑时为原机器名；新增时为空。 */
  editing?: string;
  onClose: () => void;
  onSaved: () => void;
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <fieldset className="space-y-3 rounded-md border p-3">
      <legend className="px-1 text-xs font-semibold text-muted-foreground">{title}</legend>
      {children}
    </fieldset>
  );
}

function Tri({ value, onChange, label, testId }: { value: string; onChange: (v: "" | "true" | "false") => void; label: string; testId?: string }) {
  const t = useT();
  return (
    <Field label={label}>
      <Select value={value} onChange={(e) => onChange(e.target.value as "" | "true" | "false")} data-testid={testId}>
        <option value="">{t("form.default")}</option>
        <option value="true">{t("common.yes")}</option>
        <option value="false">{t("common.no")}</option>
      </Select>
    </Field>
  );
}

export function MachineFormDialog({ initial, editing, onClose, onSaved }: Props) {
  const t = useT();
  const client = useLive((s) => s.client);
  const [f, setF] = useState<MachineForm>(initial);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [testing, setTesting] = useState(false);
  const [result, setResult] = useState<TestResult | null>(null);

  const set = <K extends keyof MachineForm>(k: K, v: MachineForm[K]) => setF((p) => ({ ...p, [k]: v }));
  const err = (k: string) => (errors[k] ? t(errors[k]) : undefined);

  const submit = async () => {
    const e = validateForm(f);
    setErrors(e);
    if (Object.keys(e).length || !client) return;
    setBusy(true);
    try {
      const def = toMachineDef(f, { editing });
      if (editing) {
        await client.updateMachine(editing, def);
        toast.success(t("machines.updated", { name: f.name }));
      } else {
        await client.createMachine(def);
        toast.success(t("machines.created", { name: f.name }));
      }
      onSaved();
      onClose();
    } catch (e) {
      toast.error(e);
    } finally {
      setBusy(false);
    }
  };

  const test = async () => {
    if (!client || !editing) return;
    setTesting(true);
    setResult(null);
    try {
      // 用表单中的值覆盖（密码仅用于本次，不保存）
      const body = {
        host: f.host.trim() || undefined,
        port: f.port ? Number(f.port) : undefined,
        username: f.username.trim() || undefined,
        password: f.password || undefined,
        security: f.security || undefined,
        width: f.width ? Number(f.width) : undefined,
        height: f.height ? Number(f.height) : undefined,
      };
      setResult(await client.testMachine(editing, body));
    } catch (e) {
      setResult({ ok: false, error: e instanceof Error ? e.message : String(e) });
    } finally {
      setTesting(false);
    }
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent wide data-testid="machine-form" onInteractOutside={(e) => e.preventDefault()}>
        <DialogHeader>
          <DialogTitle>{editing ? t("form.title_edit", { name: editing }) : t("form.title_new")}</DialogTitle>
        </DialogHeader>

        <div className="grid gap-3 md:grid-cols-2">
          <Section title={t("form.section.basic")}>
            <Field label={t("form.name")} hint={t("form.name_hint")} error={err("name")}>
              <Input value={f.name} onChange={(e) => set("name", e.target.value)} data-testid="f-name" />
            </Field>
            <Field label={t("form.description")} hint={t("form.description_hint")}>
              <Textarea value={f.description} onChange={(e) => set("description", e.target.value)} data-testid="f-description" />
            </Field>
            <Field label={t("form.tags")} hint={t("form.tags_hint")}>
              <Input value={f.tags} onChange={(e) => set("tags", e.target.value)} data-testid="f-tags" />
            </Field>
            <div className="grid grid-cols-[1fr_96px] gap-2">
              <Field label={t("form.host")} error={err("host")}>
                <Input value={f.host} onChange={(e) => set("host", e.target.value)} data-testid="f-host" />
              </Field>
              <Field label={t("form.port")} error={err("port")}>
                <Input value={f.port} placeholder="3389" onChange={(e) => set("port", e.target.value)} data-testid="f-port" />
              </Field>
            </div>
            <div className="grid grid-cols-2 gap-2">
              <Field label={t("form.username")} error={err("username")}>
                <Input value={f.username} onChange={(e) => set("username", e.target.value)} data-testid="f-username" />
              </Field>
              <Field label={t("form.domain")}>
                <Input value={f.domain} onChange={(e) => set("domain", e.target.value)} data-testid="f-domain" />
              </Field>
            </div>
            <label className="flex items-center gap-2 text-sm">
              <Switch checked={f.enabled} onCheckedChange={(v) => set("enabled", v)} data-testid="f-enabled" />
              {t("form.enabled")}
            </label>
          </Section>

          <div className="space-y-3">
            <Section title={t("form.section.password")}>
              <Field label={t("form.password")} hint={editing ? t("form.password_edit_hint") : t("form.password_new_hint")}>
                <Input
                  type="password"
                  autoComplete="new-password"
                  value={f.password}
                  disabled={f.clearPassword}
                  onChange={(e) => set("password", e.target.value)}
                  data-testid="f-password"
                />
              </Field>
              {editing && (
                <label className="flex items-center gap-2 text-sm">
                  <input type="checkbox" checked={f.clearPassword} onChange={(e) => set("clearPassword", e.target.checked)} data-testid="f-clear-password" />
                  {t("form.clear_password")}
                </label>
              )}
            </Section>

            <Section title={t("form.section.display")}>
              <div className="grid grid-cols-2 gap-2">
                <Field label={t("form.width")} error={err("width")}>
                  <Input value={f.width} placeholder="1920" onChange={(e) => set("width", e.target.value)} data-testid="f-width" />
                </Field>
                <Field label={t("form.height")} error={err("height")}>
                  <Input value={f.height} placeholder="1080" onChange={(e) => set("height", e.target.value)} data-testid="f-height" />
                </Field>
              </div>
              <p className="text-xs text-muted-foreground">{t("form.size_hint")}</p>
            </Section>

            <Section title={t("form.section.security")}>
              <div className="grid grid-cols-2 gap-2">
                <Field label={t("form.security")}>
                  <Select value={f.security} onChange={(e) => set("security", e.target.value as MachineForm["security"])} data-testid="f-security">
                    <option value="">{t("form.default")}</option>
                    <option value="auto">auto</option>
                    <option value="nla">nla</option>
                    <option value="tls">tls</option>
                    <option value="rdp">rdp</option>
                  </Select>
                </Field>
                <Field label={t("form.cert")}>
                  <Select value={f.cert} onChange={(e) => set("cert", e.target.value as MachineForm["cert"])} data-testid="f-cert">
                    <option value="">{t("form.default")}</option>
                    <option value="tofu">{t("form.cert.tofu")}</option>
                    <option value="ignore">{t("form.cert.ignore")}</option>
                    <option value="fingerprint">{t("form.cert.fingerprint")}</option>
                  </Select>
                </Field>
              </div>
              {f.cert === "fingerprint" && (
                <Field label={t("form.cert_fingerprint")} error={err("cert_fingerprint")}>
                  <Input className="mono" value={f.cert_fingerprint} onChange={(e) => set("cert_fingerprint", e.target.value)} placeholder="sha256:AB:CD:…" data-testid="f-fingerprint" />
                </Field>
              )}
            </Section>
          </div>
        </div>

        <Section title={t("form.section.advanced")}>
          <div className="grid gap-3 md:grid-cols-3">
            <Tri label={t("form.auto_connect")} value={f.auto_connect} onChange={(v) => set("auto_connect", v)} testId="f-auto-connect" />
            <Field label={t("form.idle_disconnect")} hint={t("form.idle_hint")} error={err("idle_disconnect")}>
              <Input value={f.idle_disconnect} placeholder="30m" onChange={(e) => set("idle_disconnect", e.target.value)} data-testid="f-idle" />
            </Field>
            <Field label={t("form.reconnect_attempts")} error={err("reconnect_attempts")}>
              <Input value={f.reconnect_attempts} placeholder="5" onChange={(e) => set("reconnect_attempts", e.target.value)} data-testid="f-reconnect" />
            </Field>
            <Tri label={t("form.enable_drive")} value={f.enable_drive} onChange={(v) => set("enable_drive", v)} testId="f-drive" />
            <Tri label={t("form.enable_clipboard")} value={f.enable_clipboard} onChange={(v) => set("enable_clipboard", v)} testId="f-clipboard" />
            <Tri label={t("form.disable_wallpaper")} value={f.disable_wallpaper} onChange={(v) => set("disable_wallpaper", v)} testId="f-wallpaper" />
          </div>
        </Section>

        {result && (
          <div className="space-y-2 rounded-md border p-3 text-sm" data-testid="test-result" data-ok={result.ok}>
            <div className={result.ok ? "font-medium text-success" : "font-medium text-destructive"}>
              {result.ok ? t("form.test_ok", { w: result.width ?? 0, h: result.height ?? 0 }) : `${t("form.test_failed")}: ${result.error ?? ""}`}
            </div>
            {result.freerdp && <div className="text-xs text-muted-foreground">FreeRDP {result.freerdp}</div>}
            {result.screenshot && (
              <img src={`data:image/jpeg;base64,${result.screenshot}`} alt="screenshot" className="max-h-64 rounded border" data-testid="test-screenshot" />
            )}
          </div>
        )}

        <DialogFooter className="items-center">
          <div className="mr-auto text-xs text-muted-foreground">{!editing && t("form.test_need_saved")}</div>
          <Button variant="outline" onClick={test} disabled={!editing || testing} data-testid="f-test">
            {testing && <Loader2 className="animate-spin" />}
            {testing ? t("form.testing") : t("form.test")}
          </Button>
          <Button variant="outline" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button onClick={submit} disabled={busy} data-testid="f-save">
            {t("common.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
