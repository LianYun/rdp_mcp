import { NavLink, Outlet } from "react-router-dom";
import { Activity, LayoutDashboard, Monitor, ScrollText, Settings } from "lucide-react";
import { CertDialog } from "@/components/CertDialog";
import { Toaster } from "@/components/Toaster";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";
import { useLive } from "@/stores/live";

const items = [
  { to: "/", key: "nav.overview", icon: LayoutDashboard, end: true },
  { to: "/machines", key: "nav.machines", icon: Monitor, end: false },
  { to: "/activity", key: "nav.activity", icon: Activity, end: false },
  { to: "/logs", key: "nav.logs", icon: ScrollText, end: false },
  { to: "/settings", key: "nav.settings", icon: Settings, end: false },
];

export function Layout() {
  const t = useT();
  const status = useLive((s) => s.status);
  const connected = useLive((s) => s.connected);
  const dot = status === "running" ? (connected ? "bg-success" : "bg-warning") : status === "error" ? "bg-destructive" : "bg-muted-foreground/50";

  return (
    <div className="flex h-full">
      <aside className="flex w-52 shrink-0 flex-col border-r bg-card/50 p-3">
        <div className="mb-4 flex items-center gap-2 px-2 pt-1 text-base font-semibold">
          <span className={cn("size-2.5 rounded-full", dot)} data-testid="sidebar-dot" data-status={status} />
          Remoter
        </div>
        <nav className="space-y-1">
          {items.map(({ to, key, icon: Icon, end }) => (
            <NavLink
              key={to}
              to={to}
              end={end}
              data-testid={`nav-${to === "/" ? "overview" : to.slice(1)}`}
              className={({ isActive }) =>
                cn(
                  "flex items-center gap-2 rounded-md px-2.5 py-2 text-sm transition-colors hover:bg-accent",
                  isActive && "bg-accent font-medium text-accent-foreground",
                )
              }
            >
              <Icon className="size-4" />
              {t(key)}
            </NavLink>
          ))}
        </nav>
      </aside>
      <main className="min-w-0 flex-1 overflow-y-auto">
        <Outlet />
      </main>
      <CertDialog />
      <Toaster />
    </div>
  );
}
