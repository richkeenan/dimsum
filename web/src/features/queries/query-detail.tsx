import { microsecondsToMS, text, type QueryDetail as Detail, type Row } from "@/lib/api";
import { useResource } from "@/lib/hooks";
import { Resource } from "@/components/data";
import { Button } from "@/components/ui/button";
import { QueryRuleForm } from "./rule-action";
import { ResponseRecords, ResultBadge, responseStatus } from "./response";

export function QueryDetail({
  id,
  filterIdentity,
}: {
  id: string;
  filterIdentity: (row: Row, key: "rule_id" | "source_id" | "upstream_id") => void;
}) {
  const state = useResource<Detail>("queries/" + encodeURIComponent(id));
  const row = state.data;
  return (
    <Resource state={state}>
      {row && (
        <div className="space-y-6 pb-2">
          <div className="space-y-3">
            <h3 className="pr-4 text-lg font-medium leading-snug wrap-anywhere">
              {row.name || "Root domain"}
            </h3>
            <ResultBadge outcome={row.outcome} />
          </div>
          <ResponseRecords response={row.response} />
          <section>
            <h3 className="mb-2 text-sm font-medium">Query information</h3>
            <dl className="text-sm [&>div]:grid [&>div]:grid-cols-[minmax(100px,35%)_1fr] [&>div]:gap-3 [&>div]:border-b [&>div]:border-border [&>div]:py-2.5 [&_dt]:text-muted-foreground [&_dd]:wrap-anywhere">
              <div>
                <dt>DNS status</dt>
                <dd>{responseStatus(row.rcode)}</dd>
              </div>
              <div>
                <dt>Client</dt>
                <dd>
                  {row.client_name || row.client}
                  {row.client_name && (
                    <span className="mt-1 block text-xs text-muted-foreground">{row.client}</span>
                  )}
                </dd>
              </div>
              <div>
                <dt>Time</dt>
                <dd>
                  <time dateTime={row.time} title={row.time}>
                    {new Date(row.time).toLocaleString(undefined, {
                      dateStyle: "medium",
                      timeStyle: "long",
                    })}
                  </time>
                </dd>
              </div>
              <div>
                <dt>Type</dt>
                <dd>{row.qtype}</dd>
              </div>
              <div>
                <dt>Response time</dt>
                <dd className="tabular-nums">
                  {microsecondsToMS(row.duration_us)}{" "}
                  <span className="text-muted-foreground">ms</span>
                </dd>
              </div>
              <div>
                <dt>Transport</dt>
                <dd>{row.flags & 1 ? "TCP" : "UDP"}</dd>
              </div>
              {!!(row.flags & 2) && (
                <div>
                  <dt>Shared lookup</dt>
                  <dd>Combined with an in-flight query</dd>
                </div>
              )}
              {!!(row.flags & 4) && (
                <div>
                  <dt>Fallback</dt>
                  <dd>Used an alternative upstream</dd>
                </div>
              )}
              {!!(row.flags & 8) && (
                <div>
                  <dt>DNS truncation</dt>
                  <dd>Reply was shortened to fit the transport limit</dd>
                </div>
              )}
            </dl>
          </section>
          {(row.rule_description_available || row.alias_available || row.source_id) && (
            <section className="space-y-2">
              <h3 className="text-sm font-medium">Why this result?</h3>
              {row.outcome === "blocked" && (
                <p className="text-sm leading-relaxed">
                  {row.rule_description?.includes("class: subscription-deny")
                    ? "Blocked by a subscribed blocklist."
                    : row.flags & 16
                      ? "Blocked after checking the upstream response."
                      : row.rule_description_available
                        ? "Blocked by a matching domain rule."
                        : "Blocked by DNS policy."}
                </p>
              )}
              {row.alias_available && (
                <p className="text-sm wrap-anywhere">Matched alias: {row.alias}</p>
              )}
              {row.rule_description_available && (
                <details className="rounded-md border border-border p-3 text-xs">
                  <summary className="cursor-pointer text-muted-foreground">
                    Matched rule details
                  </summary>
                  <pre className="mt-3 font-sans leading-relaxed whitespace-pre-wrap wrap-anywhere">
                    {row.rule_description}
                  </pre>
                </details>
              )}
              {row.source_id && (
                <p className="text-xs text-muted-foreground wrap-anywhere">
                  Source: {row.source_id}
                </p>
              )}
            </section>
          )}
          <div className="flex flex-wrap gap-2">
            {(["rule_id", "source_id", "upstream_id"] as const).map((key) =>
              row[key] && text(row[key]) !== "0" ? (
                <Button
                  key={key}
                  variant="outline"
                  size="sm"
                  className="text-xs"
                  onClick={() => filterIdentity(row, key)}
                >
                  Queries for this{" "}
                  {key === "rule_id" ? "rule" : key === "source_id" ? "source" : "upstream"}
                </Button>
              ) : null,
            )}
          </div>
          <QueryRuleForm name={row.name} outcome={row.outcome} address={row.client} />
        </div>
      )}
    </Resource>
  );
}
