import type { ControlMode, MachineState } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";

const stateColor: Record<MachineState, string> = {
  connected: "bg-success",
  connecting: "bg-warning animate-pulse",
  reconnecting: "bg-warning animate-pulse",
  failed: "bg-destructive",
  idle: "bg-muted-foreground/50",
};

export function StatusDot({ state, className }: { state: MachineState | "disabled"; className?: string }) {
  return (
    <span
      data-state={state}
      className={cn("inline-block size-2.5 shrink-0 rounded-full", state === "disabled" ? "bg-muted-foreground/30" : stateColor[state], className)}
    />
  );
}

export function StateBadge({ state }: { state: MachineState }) {
  const t = useT();
  const variant = state === "connected" ? "success" : state === "failed" ? "destructive" : state === "idle" ? "secondary" : "warning";
  return (
    <Badge variant={variant} data-testid="state-badge">
      <StatusDot state={state} className="size-1.5" />
      {t(`state.${state}`)}
    </Badge>
  );
}

export function ControlBadge({ mode }: { mode: ControlMode }) {
  const t = useT();
  const variant = mode === "agent" ? "default" : mode === "human" ? "warning" : "destructive";
  return (
    <Badge variant={variant} data-testid="control-badge">
      {t(`control.${mode}`)}
    </Badge>
  );
}
