import { lazy, Suspense } from "react";
import { ClientIdentity } from "@/components/client-identity";
import type { Device } from "@/lib/api";
import { useResource } from "@/lib/hooks";
import {
  count,
  percentage,
  rows,
  text,
  type Summary,
  type Series,
  type Rankings,
  type ClientsResponse,
} from "@/lib/api";
import { DataTable, Resource } from "@/components/data";
const TrafficChart = lazy(() => import("./chart"));
export default function Overview({
  range,
  resolution,
  refresh,
  drill,
}: {
  range: string;
  resolution: number;
  refresh: number;
  drill: (key: string, value: string) => void;
}) {
  const summary = useResource<Summary>("summary?" + range, refresh);
  const series = useResource<Series>(
    "timeseries?" + range + "&resolution_seconds=" + resolution,
    refresh,
  );
  const rankings = useResource<Rankings>("rankings?" + range, refresh);
  const clients = useResource<ClientsResponse>(
    "clients?" + range + "&limit=200",
    refresh,
  );
  const s = summary.data;
  return (
    <>
      <Resource state={summary}>
        <div className="mt-3 grid grid-cols-2 overflow-hidden rounded-lg border border-border bg-background min-[1101px]:grid-cols-5">
          {[
            ["Total queries", count(s?.queries)],
            ["Blocked queries", count(s?.blocked)],
            ["Blocked %", percentage(s?.blocked, s?.queries)],
            ["Answered from cache", percentage(s?.fresh, s?.queries)],
            [
              "Active clients",
              clients.data?.observed
                ? (clients.data.observed.truncated ? "≥ " : "") +
                  count(String(clients.data.observed.items?.length ?? 0))
                : "—",
            ],
          ].map(([label, value]) => (
            <div
              className="min-w-0 border-border px-4 py-4 max-[1100px]:border-b max-[1100px]:odd:border-r max-[1100px]:last:col-span-2 max-[1100px]:last:border-b-0 max-[1100px]:last:border-r-0 min-[1101px]:border-r min-[1101px]:last:border-r-0 min-[1051px]:px-5"
              key={label}
            >
              <span className="text-xs text-muted-foreground">{label}</span>
              <strong className="block text-[25px] leading-[1.4] font-[550] tracking-[-0.6px] tabular-nums min-[1051px]:text-[30px]">
                {value}
              </strong>
            </div>
          ))}
        </div>
        <div className="mt-3.5 mb-6 flex flex-wrap gap-3 text-xs text-muted-foreground min-[701px]:gap-[22px] [&_b]:ml-1 [&_b]:font-normal [&_b]:text-foreground">
          <span>
            Rejected queries <b>{count(s?.rejected)}</b>
          </span>
        </div>
      </Resource>
      <Resource state={clients}>
        {clients.data?.observed?.truncated && (
          <p className="text-sm leading-relaxed text-muted-foreground">
            Client count shows the first 200 observed addresses.
          </p>
        )}
      </Resource>
      <section className="mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background">
        <div className="flex flex-wrap items-center justify-between gap-2.5 border-b border-border p-3.5 min-[701px]:px-[18px] min-[701px]:py-[13px]">
          <h2 className="text-sm font-medium">Query activity</h2>
          <span className="text-xs text-muted-foreground">
            Outcomes over the selected range
          </span>
        </div>
        <Resource state={series}>
          <Suspense
            fallback={
              <p
                className="p-5 text-sm leading-relaxed text-muted-foreground"
                role="status"
              >
                Loading chart…
              </p>
            }
          >
            <TrafficChart buckets={series.data?.points ?? []} />
          </Suspense>
        </Resource>
      </section>
      <Resource state={rankings}>
        <div className="grid min-w-0 grid-cols-1 gap-5 min-[1051px]:grid-cols-2">
          <section className="mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background">
            <div className="flex flex-wrap items-center justify-between gap-2.5 border-b border-border p-3.5 min-[701px]:px-[18px] min-[701px]:py-[13px]">
              <h2 className="text-sm font-medium">Top clients</h2>
              <span className="text-xs text-muted-foreground">
                By requests · top 10
              </span>
            </div>
            <DataTable
              items={rows(rankings.data, "clients").slice(0, 10)}
              columns={[
                {
                  key: "name",
                  label: "Client",
                  render: (r) => (
                    <button
                      className="border-0 bg-transparent p-0 text-left text-foreground hover:underline"
                      onClick={() => drill("client", text(r.address))}
                    >
                      <ClientIdentity
                        address={text(r.address)}
                        name={r.name ? String(r.name) : undefined}
                        device={r.device as Device | undefined}
                      />
                    </button>
                  ),
                },
                {
                  key: "count",
                  label: "Requests",
                  align: "right",
                  width: 110,
                  render: (r) => count(r.count),
                },
              ]}
            />
          </section>
          <section className="mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background">
            <div className="flex flex-wrap items-center justify-between gap-2.5 border-b border-border p-3.5 min-[701px]:px-[18px] min-[701px]:py-[13px]">
              <h2 className="text-sm font-medium">Top blocked domains</h2>
              <span className="text-xs text-muted-foreground">
                Exact names · top 10
              </span>
            </div>
            <DataTable
              items={rows(rankings.data, "domains").slice(0, 10)}
              columns={[
                {
                  key: "name",
                  label: "Domain",
                  render: (r) => (
                    <button
                      className="border-0 bg-transparent p-0 text-left text-base text-foreground hover:underline"
                      onClick={() => drill("name", text(r.name))}
                    >
                      {text(r.name)}
                    </button>
                  ),
                },
                {
                  key: "count",
                  label: "Blocked",
                  align: "right",
                  width: 110,
                  render: (r) => count(r.count),
                },
              ]}
            />
          </section>
        </div>
      </Resource>
    </>
  );
}
