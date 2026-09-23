import { useState } from "react";
import { DomainInspector } from "../clients/inspect";
import { DiscoveryStatus } from "@/features/settings/discovery";
import { useResource } from "@/lib/hooks";
import { Details, Resource } from "@/components/data";
import { Button } from "@/components/ui/button";
import { rows, type Row } from "@/lib/api";
import { QueryHistoryStatus } from "./history";
export default function Diagnostics() {
  const [tick, setTick] = useState(0);
  const state = useResource<Row>("diagnostics", tick);
  const storage = state.data?.storage as Row | undefined;
  const upstreams = rows(state.data?.upstreams);
  return (
    <section className="mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background p-5 [&_p]:leading-relaxed">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-2.5 border-b border-border pb-4">
        <h2 className="text-sm font-medium">Service diagnostics</h2>
        <Button variant="outline" onClick={() => setTick((t) => t + 1)}>
          Refresh measurements
        </Button>
      </div>
      <DomainInspector />
      <Resource state={state} retry={() => setTick((t) => t + 1)}>
        <div className="my-5 min-w-0 divide-y divide-border [&>section]:grid [&>section]:min-w-0 [&>section]:gap-2 [&>section]:py-4 min-[701px]:[&>section]:grid-cols-[10rem_minmax(0,1fr)] min-[701px]:[&>section]:gap-6">
          <section>
            <h3 className="text-sm font-medium">Device discovery</h3>
            <DiscoveryStatus value={state.data?.naming as Row | undefined} />
          </section>
          <section>
            <h3 className="text-sm font-medium">DNS service</h3>
            <strong>
              {state.data?.dns_ready === true
                ? "Ready to answer queries"
                : state.data?.dns_ready === false
                  ? "Not ready"
                  : "Status unavailable"}
            </strong>
          </section>
          <section>
            <h3 className="text-sm font-medium">Query history</h3>
            <QueryHistoryStatus storage={storage} />
          </section>
          <section>
            <h3 className="text-sm font-medium">Upstream servers</h3>
            {upstreams.length ? (
              <ul className="space-y-2 text-xs [overflow-wrap:anywhere]">
                {upstreams.map((server, index) => (
                  <li key={index}>
                    <strong>{String(server.Endpoint ?? server.endpoint ?? "Server")}</strong> —{" "}
                    {(
                      {
                        closed: "Accepting queries",
                        open: "Waiting before retry",
                        "half-open": "Checking connection",
                      } as Record<string, string>
                    )[String(server.State ?? server.state)] ?? "Status unavailable"}
                    {server.Fallback === true || server.fallback === true ? " (fallback)" : ""}
                  </li>
                ))}
              </ul>
            ) : (
              <p className="mb-[18px] text-xs text-muted-foreground">
                No upstream measurements available.
              </p>
            )}
          </section>
        </div>
        <details className="min-w-0 border-t border-border px-5 py-3 text-xs">
          <summary className="cursor-pointer text-muted-foreground">Technical measurements</summary>
          <Details value={state.data} />
        </details>
      </Resource>
    </section>
  );
}
