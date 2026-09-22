import { count, percentage, type LatencyBand } from "@/lib/api";
import { formatLatency } from "./format";

export default function Distribution({
  bands,
  total,
}: {
  bands: LatencyBand[];
  total: string;
}) {
  return (
    <div className="px-5 py-4">
      <table className="w-full text-xs tabular-nums">
        <caption className="sr-only">
          Queries by response-time band. Upper limits are exclusive.
        </caption>
        <thead>
          <tr className="text-muted-foreground">
            <th className="pb-3 text-left font-normal">Response time</th>
            <th className="pb-3 text-right font-normal">Queries</th>
            <th className="pb-3 pl-4 text-right font-normal">Share</th>
          </tr>
        </thead>
        <tbody>
          {bands.map((band) => {
            const share = percentage(band.count, total);
            const label =
              band.upper_us == null
                ? `≥ ${formatLatency(band.lower_us)}`
                : band.lower_us === "0"
                  ? `< ${formatLatency(band.upper_us)}`
                  : `${formatLatency(band.lower_us)} – < ${formatLatency(band.upper_us)}`;
            return (
              <tr key={band.lower_us}>
                <th
                  scope="row"
                  className="relative py-2.5 pr-4 text-left font-normal"
                >
                  <span
                    aria-hidden="true"
                    className="absolute inset-y-1 left-0 rounded-sm bg-primary/10"
                    style={{
                      width:
                        share === "—"
                          ? "0%"
                          : percentage(band.count, total, "en-US"),
                    }}
                  />
                  <span className="relative">{label}</span>
                </th>
                <td className="py-2.5 text-right">{count(band.count)}</td>
                <td className="py-2.5 pl-4 text-right text-muted-foreground">
                  {share}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
