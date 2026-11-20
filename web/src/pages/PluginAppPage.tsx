import { useEffect, useMemo, useState } from "react";
import { useLocation, useParams } from "react-router";

import { v2 } from "@/api/v2/request";

function buildPluginProxySrc(installationId: string, routePath: string, search: string): string {
  const normalizedPath = routePath ? `/${routePath.replace(/^\/+/, "")}` : "";
  const params = new URLSearchParams(search);
  const theme = document.documentElement.dataset.theme;
  if (theme && !params.has("theme")) params.set("theme", theme);
  const query = params.toString();
  return `/api/v1/plugins/${installationId}${normalizedPath}${query ? `?${query}` : ""}`;
}

export default function PluginAppPage() {
  const { installationId = "", "*": routePath = "" } = useParams();
  const location = useLocation();
  const [launchReady, setLaunchReady] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setLaunchReady(false);
    v2("POST /api/v2/auth/plugin-launch")
      .catch((error) => {
        console.warn("plugin launch preparation failed", error);
      })
      .finally(() => {
        if (!cancelled) setLaunchReady(true);
      });
    return () => {
      cancelled = true;
    };
  }, [installationId, routePath]);

  const src = useMemo(
    () => buildPluginProxySrc(installationId, routePath, location.search),
    [installationId, routePath, location.search],
  );

  return (
    <main className="bg-background min-h-[calc(100vh-2rem)]">
      {launchReady ? (
        <iframe
          title="Plugin app"
          src={src}
          className="h-[calc(100vh-2rem)] w-full border-0"
          allow="autoplay; fullscreen; picture-in-picture"
        />
      ) : (
        <div className="text-muted-foreground flex h-[calc(100vh-2rem)] items-center justify-center text-sm">
          Opening plugin...
        </div>
      )}
    </main>
  );
}
