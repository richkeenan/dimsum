import { useState } from "react";
import {
  Camera,
  Gamepad2,
  Info,
  Laptop,
  Lightbulb,
  Monitor,
  Plug,
  Printer,
  Server,
  Smartphone,
  Speaker,
  Tablet,
  Tv,
  type LucideIcon,
} from "lucide-react";
import type { Device } from "@/lib/api";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

const categories: Record<string, [string, LucideIcon]> = {
  unknown: ["Device", Monitor],
  phone: ["Phone", Smartphone],
  tablet: ["Tablet", Tablet],
  laptop: ["Laptop", Laptop],
  desktop: ["Desktop", Monitor],
  tv: ["TV", Tv],
  speaker: ["Speaker", Speaker],
  printer: ["Printer", Printer],
  camera: ["Camera", Camera],
  lighting: ["Lighting", Lightbulb],
  appliance: ["Appliance", Plug],
  server: ["Server", Server],
  console: ["Game console", Gamepad2],
};
type Props = {
  address: string;
  name?: string;
  device?: Device;
  stale?: boolean;
};
export function ClientIdentity({ address, name, device, stale }: Props) {
  const [label, Icon] =
    categories[device?.category ?? "unknown"] ?? categories.unknown;
  return (
    <span className="inline-flex max-w-full items-start gap-2.5 text-left">
      <Icon
        role="img"
        aria-label={label}
        className="mt-1 size-4.5 shrink-0 text-muted-foreground"
        strokeWidth={1.5}
      />
      <span className="min-w-0 text-base leading-normal">
        <span className="block wrap-anywhere">{name || address}</span>
        {name && name !== address && (
          <span className="mt-0.5 block text-xs text-muted-foreground">
            {address}
            {stale ? " · stale name" : ""}
          </span>
        )}
      </span>
    </span>
  );
}
export function DeviceDetails({ device }: { device?: Device }) {
  if (!device)
    return (
      <p className="text-sm text-muted-foreground">
        No discovery metadata available.
      </p>
    );
  return (
    <div className="space-y-4 text-sm wrap-anywhere">
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2">
        <dt className="text-muted-foreground">Type</dt>
        <dd>
          {(categories[device.category] ?? categories.unknown)[0]}
          {device.inferred ? " · inferred" : ""}
        </dd>
        <dt className="text-muted-foreground">Evidence</dt>
        <dd>{device.reason}</dd>
        {device.hostname && (
          <>
            <dt className="text-muted-foreground">Hostname</dt>
            <dd>{device.hostname}</dd>
          </>
        )}
        {device.model && (
          <>
            <dt className="text-muted-foreground">Model</dt>
            <dd>{device.model}</dd>
          </>
        )}
        {device.manufacturer && (
          <>
            <dt className="text-muted-foreground">Manufacturer</dt>
            <dd>{device.manufacturer}</dd>
          </>
        )}
        <dt className="text-muted-foreground">Freshness</dt>
        <dd>{device.fresh ? "Current" : "Unavailable or expired"}</dd>
      </dl>
      {device.evidence?.length > 0 && (
        <section className="space-y-2">
          <h3 className="font-medium">Local advertisements</h3>
          <ul className="space-y-3">
            {device.evidence.map((e, i) => (
              <li key={i} className="rounded-md border border-border p-3">
                <p>{e.label || e.hostname}</p>
                <p className="text-xs text-muted-foreground">
                  {e.source}
                  {e.service_type ? ` · ${e.service_type}` : ""}
                </p>
                {e.hostname && e.label && (
                  <p className="text-xs text-muted-foreground">{e.hostname}</p>
                )}
                <p className="text-xs text-muted-foreground">
                  Expires {new Date(e.expires).toLocaleString()}
                </p>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}
export function ClientDeviceButton(props: Props & { source?: string }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => setOpen(true)}
        aria-label={`Device details for ${props.name || props.address}`}
      >
        <Info aria-hidden="true" />
        Details
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{props.name || props.address}</DialogTitle>
            <DialogDescription>
              {props.address}
              {props.source ? ` · Name source: ${props.source}` : ""}
            </DialogDescription>
          </DialogHeader>
          <DeviceDetails device={props.device} />
        </DialogContent>
      </Dialog>
    </>
  );
}
