import { Link } from "react-router-dom";
import { EmergencyStop } from "@/components/EmergencyStop";
import { MachineCard } from "@/components/MachineCard";
import { McpAccessCard } from "@/components/McpAccessCard";
import { ServiceCard } from "@/components/ServiceCard";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { useLive } from "@/stores/live";

export default function Overview() {
  const t = useT();
  const machines = useLive((s) => s.machines);
  const running = useLive((s) => s.status === "running");
  const configError = useLive((s) => s.configError);

  return (
    <div className="space-y-5 p-6" data-testid="page-overview">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">{t("nav.overview")}</h1>
        <EmergencyStop />
      </div>
      {configError && (
        <div className="rounded-md border border-destructive/50 bg-destructive/10 p-3 text-sm text-destructive" data-testid="config-error">
          {t("overview.config_error")}: {configError}
        </div>
      )}
      <div className="grid gap-4 lg:grid-cols-2">
        <ServiceCard />
        <McpAccessCard />
      </div>
      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-base font-semibold">{t("overview.machines")}</h2>
          <Button asChild variant="outline" size="sm">
            <Link to="/machines">{t("overview.manage")}</Link>
          </Button>
        </div>
        {!running ? (
          <p className="text-sm text-muted-foreground">{t("overview.service_down")}</p>
        ) : machines.length === 0 ? (
          <p className="text-sm text-muted-foreground" data-testid="no-machines">
            {t("overview.no_machines")}
          </p>
        ) : (
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3" data-testid="machine-grid">
            {machines.map((m) => (
              <MachineCard key={m.name} m={m} />
            ))}
          </div>
        )}
      </section>
    </div>
  );
}
