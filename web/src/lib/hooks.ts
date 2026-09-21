import { useEffect, useState } from "react";
import { api } from "./api";
export function useResource<T>(path: string, refresh = 0) {
  const [state, setState] = useState<{
    data?: T;
    error?: Error;
    loading: boolean;
  }>({ loading: true });
  useEffect(() => {
    const controller = new AbortController();
    setState({ loading: true });
    api
      .get<T>(path, controller.signal)
      .then((data) => {
        if (!controller.signal.aborted) setState({ data, loading: false });
      })
      .catch((error) => {
        if (!controller.signal.aborted) setState({ error, loading: false });
      });
    return () => controller.abort();
  }, [path, refresh]);
  return state;
}
// Events carry invalidations only. Never accumulate history in browser memory.
export function useLive(enabled: boolean, invalidate: () => void) {
  const [connection, setConnection] = useState("Paused");
  useEffect(() => {
    if (!enabled) {
      setConnection("Paused");
      return;
    }
    setConnection("Connecting");
    const source = new EventSource("/api/v1/events", { withCredentials: true });
    let last = 0;
    const update = () => {
      const now = Date.now();
      if (now - last >= 1000) {
        last = now;
        invalidate();
      }
    };
    source.onopen = () => {
      setConnection("Live");
      update();
    };
    source.onmessage = update;
    for (const event of ["summary", "generation", "job", "reset", "status"])
      source.addEventListener(event, update);
    source.onerror = () =>
      setConnection("Reconnecting; displayed data may be stale");
    return () => source.close();
  }, [enabled, invalidate]);
  return connection;
}
