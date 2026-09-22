import { microsecondsToMS, text, type QueryDetail, type Row } from "@/lib/api";

export type ResponseSummary = NonNullable<QueryDetail["response"]>;

export function resultLabel(value: unknown) {
  return (
    (
      {
        local: "Local answer",
        blocked: "Blocked",
        cache: "Cached",
        stale: "Cached (stale)",
        forwarded: "Forwarded",
        error: "Failed",
        rejected: "Rejected",
      } as Record<string, string>
    )[text(value)] ?? text(value)
  );
}

export function ResultBadge({
  outcome,
  compact = false,
}: {
  outcome: unknown;
  compact?: boolean;
}) {
  return (
    <span
      className={`inline-block w-fit rounded px-[7px] py-[3px] ${compact ? "text-[12px]" : "text-xs"} ${outcome === "blocked" ? "bg-amber-100 text-amber-900 dark:bg-amber-950 dark:text-amber-200" : outcome === "error" || outcome === "rejected" ? "bg-destructive/10 text-destructive" : outcome === "stale" ? "bg-muted text-muted-foreground" : "bg-accent text-foreground"}`}
    >
      {resultLabel(outcome)}
    </span>
  );
}

export function ResponseTime({ value }: { value: unknown }) {
  const us =
    typeof value === "string" && /^\d+$/.test(value) ? Number(value) : NaN;
  if (!Number.isFinite(us))
    return <span className="text-muted-foreground">—</span>;
  const ms = us / 1000;
  const unit = ms >= 1000 ? "s" : "ms";
  const number =
    ms > 0 && ms < 0.01
      ? "<" + (0.01).toLocaleString()
      : (ms >= 1000 ? ms / 1000 : ms).toLocaleString(undefined, {
          minimumFractionDigits: ms === 0 ? 0 : ms < 1 || ms >= 1000 ? 2 : 1,
          maximumFractionDigits: ms < 1 || ms >= 1000 ? 2 : 1,
        });
  return (
    <span
      className="inline-flex items-baseline gap-1 whitespace-nowrap text-[12px] tabular-nums"
      title={`${microsecondsToMS(value)} ms`}
    >
      <span>{number}</span>{" "}
      <span className="text-muted-foreground">{unit}</span>
    </span>
  );
}

export function responseStatus(value: unknown) {
  return (
    (
      {
        0: "NOERROR · No DNS error",
        1: "FORMERR · Invalid request",
        2: "SERVFAIL · Lookup failed",
        3: "NXDOMAIN · Domain not found",
        4: "NOTIMP · Not supported",
        5: "REFUSED · Request refused",
        16: "BADVERS · Unsupported EDNS version",
      } as Record<string, string>
    )[text(value)] ?? (value == null ? "Not recorded" : `RCODE ${text(value)}`)
  );
}

export function AnswerPreview({
  row,
  inspect,
}: {
  row: Row;
  inspect: () => void;
}) {
  const response = row.response as ResponseSummary | undefined;
  if (!response)
    return (
      <span className="text-[12px] text-muted-foreground">Not recorded</span>
    );
  const answers = response.records.filter(
    (record) => record.section === "answer",
  );
  const addresses = answers.filter(
    (record) => record.type === "A" || record.type === "AAAA",
  );
  const shown = addresses.length ? addresses : answers;
  if (!shown.length)
    return (
      <span className="text-[12px] text-muted-foreground">
        {response.truncated
          ? "Partial response"
          : row.rcode === 3
            ? "Domain not found"
            : "No answers"}
      </span>
    );
  return (
    <button
      onClick={inspect}
      className="block w-full min-w-0 max-w-full text-left text-[13px] hover:underline focus-visible:outline-ring"
      title={shown.map((record) => record.value).join("\n")}
    >
      <span className="block truncate font-mono">{shown[0].value}</span>
      {(shown.length > 1 || response.truncated) && (
        <span className="text-[12px] text-muted-foreground">
          {shown.length > 1
            ? `+${(shown.length - 1).toLocaleString()} more`
            : ""}
          {response.truncated ? " · partial" : ""}
        </span>
      )}
    </button>
  );
}

export function ResponseRecords({ response }: { response?: ResponseSummary }) {
  return (
    <section className="space-y-3" aria-label="DNS response">
      <div>
        <h3 className="text-sm font-medium">DNS response</h3>
      </div>
      {!response ? (
        <p className="rounded-md bg-muted p-3 text-sm text-muted-foreground">
          Answer data wasn’t recorded for this query.
        </p>
      ) : (
        <>
          {!response.records.some((record) => record.section === "answer") && (
            <p className="text-sm text-muted-foreground">
              {response.truncated
                ? "No answer records in the captured portion."
                : "No answer records returned."}
            </p>
          )}
          {response.records.length > 0 && (
            <div className="divide-y divide-border rounded-md border border-border">
              {response.records.map((record, index) => (
                <div key={index} className="space-y-1.5 px-3 py-3">
                  <div className="flex items-baseline justify-between gap-3 text-xs">
                    <span className="font-medium">
                      {record.type}
                      {record.section !== "answer" && (
                        <span className="ml-2 font-normal text-muted-foreground">
                          {record.section}
                        </span>
                      )}
                    </span>
                    <span
                      className="shrink-0 text-muted-foreground tabular-nums"
                      title="TTL when answered"
                    >
                      {record.ttl.toLocaleString()} s
                    </span>
                  </div>
                  <p className="font-mono text-sm whitespace-pre-wrap wrap-anywhere">
                    {record.value}
                  </p>
                  <p className="text-xs text-muted-foreground wrap-anywhere">
                    {record.name}
                  </p>
                </div>
              ))}
            </div>
          )}
          {response.truncated && (
            <p className="text-xs leading-relaxed text-muted-foreground">
              Partial response: some records or long values were not retained.
            </p>
          )}
        </>
      )}
    </section>
  );
}
