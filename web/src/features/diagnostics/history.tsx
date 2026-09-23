import { InfoDetails } from "@/components/info-details";
import { count, type Row } from "@/lib/api";

function storageAdvice(error: string, unavailable: boolean) {
  const next = unavailable
    ? " Then restart Dimsum and refresh this page."
    : " Dimsum will retry; refresh to check the status.";
  if (/disk is full|no space left|SQLITE_FULL/i.test(error))
    return "Free up disk space on the server." + next;
  if (/permission denied|read.?only|SQLITE_READONLY|SQLITE_PERM/i.test(error))
    return "Check that the Dimsum service has permission to write to its data directory." + next;
  if (/database is locked|database is busy|SQLITE_BUSY|SQLITE_LOCKED/i.test(error))
    return unavailable
      ? "Check whether another process is holding the history database open." + next
      : "Dimsum will retry. If this persists, check whether another process is holding the history database open.";
  if (unavailable)
    return "Share the technical details with your administrator or support. After the storage problem is fixed, restart Dimsum and refresh this page.";
  return "Refresh to check again. If this persists, share the technical details with your administrator or support.";
}

export function QueryHistoryStatus({ storage }: { storage?: Row }) {
  const writer = storage?.writer as Row | undefined;
  const error = String(storage?.error || writer?.LastError || "");
  const unavailable = storage?.available === false;
  const known = typeof storage?.available === "boolean";
  const maintenance = /^(retention|checkpoint):/.test(error);
  const saved =
    typeof writer?.LastSuccess === "string" &&
    !writer.LastSuccess.startsWith("0001-") &&
    Number.isFinite(Date.parse(writer.LastSuccess))
      ? writer.LastSuccess
      : undefined;
  const lost = String(writer?.LostDetails ?? "0");
  const hasLoss = /^\d+$/.test(lost) && /[1-9]/.test(lost);
  const message = unavailable
    ? "Query history is unavailable"
    : !known
      ? "Query history status is unavailable"
      : error
        ? maintenance
          ? "History maintenance needs attention"
          : "Recent queries may be missing from history"
        : saved
          ? "Query history saved"
          : "Waiting for query history to be saved";
  return (
    <div className="min-w-0 space-y-2">
      <div className="flex items-center justify-between gap-2">
        <p className="text-sm">{message}</p>
        <InfoDetails label="Query history details">
          <p>Dimsum saves query history separately from answering DNS requests.</p>
          <p>
            {saved
              ? `Last successful save: ${new Date(saved).toLocaleString()}`
              : "No successful save reported yet."}
          </p>
          {hasLoss && (
            <p>
              {count(lost)} query details were lost during earlier write failures in this server
              session.
            </p>
          )}
          {writer?.Backlogged === true && (
            <p>
              Old-history cleanup is catching up. This does not mean new queries are waiting to be
              saved.
            </p>
          )}
          {error && <p className="font-mono whitespace-pre-wrap">{error}</p>}
        </InfoDetails>
      </div>
      {saved && !unavailable && (
        <p className="text-xs text-muted-foreground">
          Last saved <time dateTime={saved}>{new Date(saved).toLocaleString()}</time>
        </p>
      )}
      {(error || !known || unavailable) && (
        <p className="text-xs text-muted-foreground">{storageAdvice(error, unavailable)}</p>
      )}
      {hasLoss && !unavailable && (
        <p className="text-xs text-muted-foreground">
          {count(lost)} query details could not be saved earlier.
        </p>
      )}
    </div>
  );
}
