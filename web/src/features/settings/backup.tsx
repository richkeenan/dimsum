import { useEffect, useRef, useState } from "react";
import { api, backupURL, type Job } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { InfoDetails } from "@/components/info-details";

export function BackupDownload({
  jobs,
  refresh,
  pollError,
}: {
  jobs: Job[];
  refresh: () => void;
  pollError?: Error;
}) {
  const [requested, setRequested] = useState<Job>();
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<Error>();
  const downloaded = useRef<string | undefined>(undefined);
  const job = jobs.find((item) => item.id === requested?.id) ?? requested;
  const preparing = creating || job?.state === "running";
  const url = job?.state === "succeeded" ? backupURL(job.result) : undefined;
  const failure =
    error?.message ||
    (job?.state === "failed" ? job.error || "Backup job failed." : "") ||
    (job?.state === "succeeded" && !url
      ? "The backup job returned no valid download address."
      : "");

  useEffect(() => {
    if (!url || !job || downloaded.current === job.id) return;
    downloaded.current = job.id;
    const link = document.createElement("a");
    link.href = url;
    link.download = "dimsum-config.tar";
    document.body.append(link);
    link.click();
    link.remove();
  }, [url, job]);

  async function download() {
    if (preparing) return;
    setCreating(true);
    setError(undefined);
    setRequested(undefined);
    try {
      setRequested(await api.send<Job>("jobs", "POST", { kind: "backup", input: {} }));
      refresh();
    } catch (e) {
      setError(e as Error);
    } finally {
      setCreating(false);
    }
  }

  return (
    <section className="mb-5 min-w-0 rounded-lg border border-border bg-background p-5">
      <h2 className="mb-3 text-sm font-medium">Back up your configuration</h2>
      <p className="mb-4 text-xs text-muted-foreground">
        Download your configuration and secrets as a .tar archive. Query history, downloaded lists
        and DHCP leases are not included.
      </p>
      <Button className="min-w-44" disabled={preparing} onClick={() => void download()}>
        {preparing ? "Preparing backup…" : "Download backup"}
      </Button>
      <div
        className="mt-2 flex min-h-16 items-center gap-1 text-xs text-muted-foreground sm:min-h-9"
        role="status"
      >
        {failure ? (
          <>
            <span>Couldn’t prepare the backup. Try downloading again.</span>
            <InfoDetails label="Backup error details">
              <p>{failure}</p>
            </InfoDetails>
          </>
        ) : preparing && pollError ? (
          <>
            <span>Waiting for the server. Checking again automatically.</span>
            <InfoDetails label="Backup connection details">
              <p>{pollError.message}</p>
            </InfoDetails>
          </>
        ) : preparing ? (
          <span>Your download will start when the backup is ready.</span>
        ) : url ? (
          <span>
            Backup ready. Check your browser’s downloads.{" "}
            <a className="underline underline-offset-2" href={url} download="dimsum-config.tar">
              Download again
            </a>
          </span>
        ) : (
          <span>The file downloads to this device.</span>
        )}
      </div>
    </section>
  );
}
