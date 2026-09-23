import type { ReactNode } from "react";
import { Info } from "lucide-react";
import { Popover } from "radix-ui";
import { Button } from "./ui/button";

export function InfoDetails({ label, children }: { label: string; children: ReactNode }) {
  return (
    <Popover.Root>
      <Popover.Trigger asChild>
        <Button type="button" variant="ghost" size="icon" aria-label={label}>
          <Info aria-hidden="true" strokeWidth={1.5} />
        </Button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          aria-label={label}
          align="start"
          sideOffset={8}
          collisionPadding={16}
          className="z-50 w-96 max-w-[calc(100vw-2rem)] max-h-[var(--radix-popover-content-available-height)] overflow-y-auto rounded-lg border border-border bg-background p-4 text-xs leading-relaxed shadow-lg outline-none wrap-anywhere"
        >
          <h3 className="mb-2 font-medium">{label}</h3>
          <div className="space-y-3 text-muted-foreground">{children}</div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}
