import { useState } from "react";
import { useResource } from "@/lib/hooks";
import { Details, Resource } from "@/components/data";
import { Button } from "@/components/ui/button";
import { rows, type Row } from "@/lib/api";
export default function Diagnostics() {
  const [tick, setTick] = useState(0);
  const state = useResource<Row>("diagnostics", tick);
  const storage = state.data?.storage as Row | undefined;
  const upstreams = rows(state.data?.upstreams);
  return (
    <section className="panel inset">
      <div className="panel-heading">
        <h2>Service diagnostics</h2>
        <Button variant="outline" onClick={() => setTick((t) => t + 1)}>
          Refresh measurements
        </Button>
      </div>
      <p>
        Check DNS availability and query-history storage. For a connection test,
        use Probe on the upstream servers page.
      </p>
      <Resource state={state} retry={() => setTick((t) => t + 1)}>
        <div className="form-grid">
          <section className="panel inset">
            <h3>DNS service</h3>
            <strong>
              {state.data?.dns_ready === true
                ? "Ready to answer queries"
                : state.data?.dns_ready === false
                  ? "Not ready"
                  : "Status unavailable"}
            </strong>
          </section>
          <section className="panel inset">
            <h3>Query history</h3>
            <strong>
              {storage?.available === true
                ? "Storage available"
                : storage?.available === false
                  ? "Storage unavailable"
                  : "Status unavailable"}
            </strong>
            {storage?.error ? (
              <p role="alert">{String(storage.error)}</p>
            ) : null}
          </section>
          <section className="panel inset">
            <h3>Upstream servers</h3>
            {upstreams.length ? (
              <ul>
                {upstreams.map((server, index) => (
                  <li key={index}>
                    <strong>
                      {String(server.Endpoint ?? server.endpoint ?? "Server")}
                    </strong>{" "}
                    —{" "}
                    {(
                      {
                        closed: "Accepting queries",
                        open: "Waiting before retry",
                        "half-open": "Checking connection",
                      } as Record<string, string>
                    )[String(server.State ?? server.state)] ??
                      "Status unavailable"}
                    {server.Fallback === true || server.fallback === true
                      ? " (fallback)"
                      : ""}
                  </li>
                ))}
              </ul>
            ) : (
              <p>No upstream measurements available.</p>
            )}
          </section>
        </div>
        <details>
          <summary>Technical measurements</summary>
          <Details value={state.data} />
        </details>
      </Resource>
    </section>
  );
}
