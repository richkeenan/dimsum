import { lazy, Suspense } from "react";
import { useResource } from "@/lib/hooks";
import {
  count,
  percentage,
  rows,
  text,
  type Row,
  type Summary,
} from "@/lib/api";
import { Completeness, DataTable, Resource } from "@/components/data";
const TrafficChart = lazy(() => import("./chart"));
export default function Overview({
  range,
  refresh,
  drill,
}: {
  range: string;
  refresh: number;
  drill: (key: string, value: string) => void;
}) {
  const summary = useResource<Summary>("summary?" + range, refresh);
  const series = useResource<Row>("timeseries?" + range, refresh);
  const rankings = useResource<Row>("rankings?" + range + "&limit=10", refresh);
  const s = summary.data;
  return (
    <>
      <Resource state={summary}>
        <Completeness meta={s} />
        <div className="metrics">
          {[
            ["Total queries", count(s?.total)],
            ["Blocked", percentage(s?.blocked, s?.total)],
            ["Fresh cache", percentage(s?.cached, s?.total)],
            ["Active clients", count(s?.active_clients)],
          ].map(([label, value]) => (
            <div className="metric" key={label}>
              <span>{label}</span>
              <strong>{value}</strong>
            </div>
          ))}
        </div>
        <div className="healthline">
          <span>
            DNS <b>{text(s?.dns)}</b>
          </span>
          <span>
            Lists <b>{text(s?.list_health)}</b>
          </span>
          <span>
            Statistics <b>{text(s?.storage_health)}</b>
          </span>
          <span>
            Stale <b>{count(s?.stale)}</b>
          </span>
          <span>
            Errors <b>{count(s?.errors)}</b>
          </span>
          <span>
            Blocking{" "}
            <b>
              {s?.blocking === undefined
                ? "Unknown"
                : s.blocking
                  ? "Enabled"
                  : "Paused"}
            </b>
          </span>
        </div>
      </Resource>
      <section className="panel">
        <div className="panel-heading">
          <h2>Query activity</h2>
          <span className="muted">Outcomes over the selected range</span>
        </div>
        <Resource state={series}>
          <Completeness meta={series.data} />
          <Suspense fallback={<p role="status">Loading chart…</p>}>
            <TrafficChart buckets={rows(series.data, "buckets")} />
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
                      {text(r.name ?? r.address)}
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
