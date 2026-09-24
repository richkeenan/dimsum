import { useCallback, useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { historyWindow } from "./api";

// One owner for the dashboard clock and explicit toolbar reloads. Changing a
// query key fetches automatically; reload invalidation happens only after the
// displayed observers have committed their new keys.
export function useHistoryWindow(preset: string, custom?: { from: string; to: string }) {
  const client = useQueryClient();
  const [window, setWindow] = useState(() => ({ preset, anchor: Date.now(), reload: 0 }));
  if (window.preset !== preset) {
    setWindow({ ...window, preset, anchor: Date.now() });
  }
  const advance = useCallback(() => {
    setWindow((current) => ({ ...current, anchor: Date.now() }));
  }, []);
  const reload = useCallback(() => {
    setWindow((current) => ({ ...current, anchor: Date.now(), reload: current.reload + 1 }));
  }, []);
  useEffect(() => {
    if (window.reload === 0) return;
    void client.invalidateQueries(
      { queryKey: ["api"], refetchType: "active" },
      // Reuse requests already started for the new window or by another
      // observer. Fixed windows and non-history resources still refetch once.
      { cancelRefetch: false },
    );
  }, [client, window.reload]);
  return { ...historyWindow(preset, window.anchor, custom), advance, reload };
}
