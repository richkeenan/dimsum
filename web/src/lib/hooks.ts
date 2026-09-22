import { useEffect, useRef, useState } from "react";
import {
  keepPreviousData,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { api, APIError } from "./api";

export function useResource<T>(path: string, refresh = 0) {
  const client = useQueryClient();
  const previousRefresh = useRef(refresh);
  const resource = path.split("?")[0];
  const query = useQuery({
    queryKey: ["api", resource, path],
    queryFn: ({ signal }) => api.get<T>(path, signal),
    staleTime: 1000,
    gcTime: 60_000,
    placeholderData: keepPreviousData,
    refetchInterval: [
      "jobs",
      "diagnostics",
      "settings",
      "blocking",
      "dhcp",
      "dhcp/status",
      "dhcp/reservations",
    ].includes(resource)
      ? 5000
      : false,
    refetchIntervalInBackground: false,
    retry: (count, error) =>
      !(
        error instanceof APIError &&
        error.status >= 400 &&
        error.status < 500
      ) && count < 1,
  });
  useEffect(() => {
    if (previousRefresh.current !== refresh) {
      previousRefresh.current = refresh;
      void client.invalidateQueries(
        { queryKey: ["api", resource, path], exact: true },
        { cancelRefetch: false },
      );
    }
  }, [client, refresh, resource, path]);
  return {
    data: query.data,
    error: query.error ?? undefined,
    loading: query.isPending,
    isFetching: query.isFetching,
    isPlaceholderData: query.isPlaceholderData,
    updatedAt: query.dataUpdatedAt,
    reload: query.refetch,
    refetch: query.refetch,
  };
}

// UI invalidation only: Query deduplicates/cancels the actual resource requests.
// Historic pages and open details disable this hook at the calling view.
export function useLive(
  enabled: boolean,
  invalidate: () => void,
  interval = 2000,
) {
  const callback = useRef(invalidate);
  callback.current = invalidate;
  const [visible, setVisible] = useState(
    () =>
      typeof document === "undefined" || document.visibilityState !== "hidden",
  );
  const [online, setOnline] = useState(
    () => typeof navigator === "undefined" || navigator.onLine,
  );
  useEffect(() => {
    const visibility = () => {
      setVisible(document.visibilityState !== "hidden");
    };
    const connection = () => setOnline(navigator.onLine);
    document.addEventListener("visibilitychange", visibility);
    window.addEventListener("online", connection);
    window.addEventListener("offline", connection);
    return () => {
      document.removeEventListener("visibilitychange", visibility);
      window.removeEventListener("online", connection);
      window.removeEventListener("offline", connection);
    };
  }, []);
  useEffect(() => {
    if (!enabled || !visible || !online) return;
    const timer = window.setInterval(() => callback.current(), interval);
    return () => window.clearInterval(timer);
  }, [enabled, visible, online, interval]);
  return !enabled
    ? "Paused"
    : !online
      ? "Offline"
      : !visible
        ? "Paused in background"
        : "Live";
}
