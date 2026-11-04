import { useEffect } from "react";
import { HashRouter, Route, Routes } from "react-router-dom";
import { Layout } from "@/components/Layout";
import Activity from "@/pages/Activity";
import Logs from "@/pages/Logs";
import MachineDetail from "@/pages/MachineDetail";
import Machines from "@/pages/Machines";
import Overview from "@/pages/Overview";
import Settings from "@/pages/Settings";
import { startDiscoveryPolling } from "@/stores/live";
import { usePrefs, watchSystemTheme } from "@/stores/prefs";

export default function App() {
  useEffect(() => {
    void usePrefs.getState().load();
    const stopTheme = watchSystemTheme();
    const stopPoll = startDiscoveryPolling();
    return () => {
      stopTheme();
      stopPoll();
    };
  }, []);

  return (
    <HashRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<Overview />} />
          <Route path="machines" element={<Machines />} />
          <Route path="machines/:name" element={<MachineDetail />} />
          <Route path="activity" element={<Activity />} />
          <Route path="logs" element={<Logs />} />
          <Route path="settings" element={<Settings />} />
        </Route>
      </Routes>
    </HashRouter>
  );
}
