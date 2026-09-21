import { lazy, Suspense } from "react";
import { useResource } from "@/lib/hooks";
import {
  count,
  percentage,
  rows,
  text,
  type Row,
  type Summary,
  type Series,
  type Rankings,
  type ClientsResponse,
} from "@/lib/api";
import { Completeness, DataTable, Resource } from "@/components/data";
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
        <Completeness meta={s} />
        <div className="metrics">
          {[
            ["Admitted queries", count(s?.queries)],
            ["Blocked", percentage(s?.blocked, s?.queries)],
            ["Fresh cache", percentage(s?.fresh, s?.queries)],
            [
              "Observed clients",
              clients.data?.observed
                ? (clients.data.observed.truncated ? "≥ " : "") +
                  count(String(clients.data.observed.items?.length ?? 0))
                : "—",
            ],
          ].map(([label, value]) => (
            <div className="metric" key={label}>
              <span>{label}</span>
              <strong>{value}</strong>
            </div>
          ))}
        </div>
        <div className="healthline">
          <span>
            Stale <b>{count(s?.stale)}</b>
          </span>
          <span>
            Rejected admissions <b>{count(s?.rejected)}</b>
          </span>
        </div>
      </Resource>
      <Resource state={clients}>
        <Completeness meta={clients.data?.observed} />
      </Resource>
      <Health refresh={refresh} />
      <section className="panel">
        <div className="panel-heading">
          <h2>Query activity</h2>
          <span className="muted">Outcomes over the selected range</span>
        </div>
        <Resource state={series}>
          <Completeness meta={series.data} />
          <Suspense fallback={<p role="status">Loading chart…</p>}>
            <TrafficChart buckets={series.data?.points ?? []} />
          </Suspense>
        </Resource>
      </section>
      <Resource state={rankings}>
        <Completeness meta={rankings.data} />
        <div className="split">
          <section className="panel">
            <div className="panel-heading">
              <h2>Top clients</h2>
              <span>By requests · top 10</span>
            </div>
            <DataTable
              items={rows(rankings.data, "clients").slice(0, 10)}
              columns={[
                {
                  key: "name",
                  label: "Client",
                  render: (r) => (
                    <button
                      className="text-button"
                      onClick={() => drill("client", text(r.address))}
                    >
                      {text(r.name || r.address)}
                      <small>{text(r.address)}</small>
                    </button>
                  ),
                },
                {
                  key: "count",
                  label: "Requests",
                  render: (r) => count(r.count),
                },
              ]}
            />
          </section>
          <section className="panel">
            <div className="panel-heading">
              <h2>Top blocked domains</h2>
              <span>Exact names · top 10</span>
            </div>
            <DataTable
              items={rows(rankings.data, "domains").slice(0, 10)}
              columns={[
                {
                  key: "name",
                  label: "Domain",
                  render: (r) => (
                    <button
                      className="text-button dns"
                      onClick={() => drill("name", text(r.name))}
                    >
                      {text(r.name)}
                    </button>
                  ),
                },
                {
                  key: "count",
                  label: "Blocked",
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
function Health({ refresh }: { refresh: number }) {
  const diagnostics = useResource<Row>("diagnostics", refresh);
  const blocking = useResource<Row>("blocking", refresh);
  const storage = diagnostics.data?.storage as Row | undefined;
  const writer = storage?.writer as Row | undefined;
  return (
    <div className="healthline">
      <Resource state={diagnostics}>
        <span>
          DNS{" "}
          <b>
            {diagnostics.data?.dns_ready === true
              ? "Ready"
              : diagnostics.data?.dns_ready === false
                ? "Not ready"
                : "Unavailable"}
          </b>
        </span>
        <span>
          Statistics{" "}
          <b>
            {storage?.available === false
              ? "Unavailable"
              : writer?.LastError
                ? "Writer error"
                : storage?.available === true
                  ? "Available"
                  : "Unknown"}
          </b>
        </span>
      </Resource>
      <Resource state={blocking}>
        <span>
          Blocking{" "}
          <b>
            {blocking.data?.enabled === true
              ? "Enabled"
              : blocking.data?.enabled === false
                ? "Paused"
                : "Unknown"}
          </b>
        </span>
        {blocking.data?.enabled === false && !!blocking.data?.pause_until && (
          <span>Pause until {text(blocking.data.pause_until)}</span>
        )}
      </Resource>
    </div>
  );
}
