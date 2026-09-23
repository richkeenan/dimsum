import { count, percentage, type LatencyBand } from "@/lib/api";
import { formatLatency } from "./format";
import { SortableHead, useSortableTable } from "@/components/table-sorting";

export default function Distribution({ bands, total }: { bands: LatencyBand[]; total: string }) {
  const table = useSortableTable({
    items: bands,
    columns: [
      { key: "lower_us", label: "Response time", sortType: "number", sortDescFirst: false },
      { key: "count", label: "Queries", sortType: "number" },
      {
        key: "share",
        label: "Share",
        sortType: "number",
        sortValue: (band) => (total === "0" ? undefined : band.count),
      },
    ],
    getRowId: (band) => band.lower_us,
    initialSorting: [{ id: "lower_us", desc: false }],
  });
  return (
    <div className="px-5 py-4">
      <table className="w-full text-xs tabular-nums">
        <caption className="sr-only">
          Queries by response-time band. Upper limits are exclusive.
        </caption>
        <thead>
          <tr className="text-muted-foreground">
            {table.getHeaderGroups()[0].headers.map((header, i) => (
              <SortableHead
                key={header.id}
                column={header.column}
                sorted={header.column.getIsSorted()}
                label={String(header.column.columnDef.header)}
                align={i ? "right" : "left"}
                className={`px-0 pb-3 text-xs font-normal text-muted-foreground ${i === 2 ? "pl-4" : ""}`}
              />
            ))}
          </tr>
        </thead>
        <tbody>
          {table.getRowModel().rows.map(({ original: band }) => {
            const share = percentage(band.count, total);
            const label =
              band.upper_us == null
                ? `≥ ${formatLatency(band.lower_us)}`
                : band.lower_us === "0"
                  ? `< ${formatLatency(band.upper_us)}`
                  : `${formatLatency(band.lower_us)} – < ${formatLatency(band.upper_us)}`;
            return (
              <tr key={band.lower_us}>
                <th scope="row" className="relative py-2.5 pr-4 text-left font-normal">
                  <span
                    aria-hidden="true"
                    className="absolute inset-y-1 left-0 rounded-sm bg-primary/10"
                    style={{
                      width: share === "—" ? "0%" : percentage(band.count, total, "en-US"),
                    }}
                  />
                  <span className="relative">{label}</span>
                </th>
                <td className="py-2.5 text-right">{count(band.count)}</td>
                <td className="py-2.5 pl-4 text-right text-muted-foreground">{share}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
