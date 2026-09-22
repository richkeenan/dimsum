import type { DHCPConfigResponse, DHCPSettings } from "@/lib/api";

export function setupComplete(config: DHCPSettings) {
  return (
    [
      config.interface,
      config.server_ip,
      config.gateway,
      config.subnet,
      config.range_start,
      config.range_end,
      config.local_domain,
    ].every((v) => v.trim() !== "") && Number(config.lease_seconds) >= 60
  );
}

export function SetupSummary({
  config,
  value,
}: {
  config: DHCPSettings;
  value: DHCPConfigResponse;
}) {
  const suggested =
    !value.config.enabled && (value.setup?.suggested.length ?? 0) > 0;
  const detectedAddress =
    config.interface === value.setup?.config.interface &&
    config.server_ip === value.setup?.config.server_ip;
  const seconds = Number(config.lease_seconds);
  const duration =
    seconds % 86400 === 0
      ? `${seconds / 86400} day${seconds === 86400 ? "" : "s"}`
      : seconds % 3600 === 0
        ? `${seconds / 3600} hours`
        : `${seconds} seconds`;
  return (
    <div className="py-5">
      <dl className="grid gap-x-6 gap-y-5 text-xs sm:grid-cols-2 [&_dt]:text-muted-foreground [&_dd]:mt-1 [&_dd]:font-medium [&_dd]:wrap-anywhere">
        <div>
          <dt>Router</dt>
          <dd>{config.gateway || "Not detected"}</dd>
        </div>
        <div>
          <dt>dimsum server</dt>
          <dd>
            {config.server_ip || "Not detected"}
            {config.interface && (
              <span className="font-normal text-muted-foreground">
                {" "}
                · {config.interface}
              </span>
            )}
          </dd>
        </div>
        <div>
          <dt>
            Device addresses
            {suggested && value.setup?.suggested.includes("range_start")
              ? " · suggested"
              : ""}
          </dt>
          <dd>
            {config.range_start && config.range_end
              ? `${config.range_start} – ${config.range_end}`
              : "Choose an address range"}
          </dd>
        </div>
        <div>
          <dt>Lease duration · Local domain</dt>
          <dd>
            {seconds ? duration : "Not set"} ·{" "}
            {config.local_domain || "Not set"}
          </dd>
        </div>
      </dl>
      {!value.config.enabled && detectedAddress && (
        <p className="mt-4 text-xs text-muted-foreground">
          {value.setup?.fixed_address === "yes"
            ? "Fixed server address detected."
            : value.setup?.fixed_address === "no"
              ? "This server gets its address automatically. Set a fixed address in the server’s network settings before enabling DHCP."
              : "Check that this server has a fixed IP address in its network settings before enabling DHCP."}
        </p>
      )}
      {!setupComplete(config) && value.setup?.message && (
        <p className="mt-4 text-xs" role="status">
          {value.setup.message}
        </p>
      )}
      {suggested && value.setup?.suggested.includes("range_start") && (
        <p className="mt-3 text-xs text-muted-foreground">
          Check the suggested range against your router’s existing leases and
          reservations.
        </p>
      )}
    </div>
  );
}
