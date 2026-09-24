import { useState, type RefObject } from "react";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { PolicyEditor } from "./policy";

export function ProfileEditorDialog({
  id,
  name,
  onDirty,
  confirmLeave,
  onClose,
  onDeleted,
  returnFocus,
}: {
  id: string;
  name: string;
  onDirty: (dirty: boolean) => void;
  confirmLeave: () => boolean;
  onClose: () => void;
  onDeleted: () => void;
  returnFocus: RefObject<HTMLElement | null>;
}) {
  const [actions, setActions] = useState<HTMLDivElement | null>(null);
  const [busy, setBusy] = useState(false);
  const close = () => {
    if (!busy && confirmLeave()) onClose();
  };
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
    >
      <DialogContent
        showCloseButton={false}
        className="flex h-dvh max-h-dvh max-w-full flex-col gap-0 overflow-hidden rounded-none p-0 sm:h-[min(90dvh,60rem)] sm:max-w-[min(72rem,calc(100%-4rem))] sm:rounded-xl"
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          returnFocus.current?.focus({ preventScroll: true });
        }}
      >
        <header className="shrink-0 space-y-3 border-b border-border bg-background p-4 sm:px-6">
          <div className="flex items-center justify-between gap-3">
            <DialogTitle className="min-w-0 wrap-anywhere">Edit {name} profile</DialogTitle>
            <Button variant="outline" disabled={busy} onClick={close}>
              Cancel
            </Button>
          </div>
          <DialogDescription>
            Settings apply to every device assigned to this profile.
          </DialogDescription>
          <div ref={setActions} />
        </header>
        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-4 sm:p-6">
          <PolicyEditor
            scope="profile"
            id={id}
            onDirty={onDirty}
            onBusy={setBusy}
            actionsTarget={actions}
            onDeleted={onDeleted}
          />
        </div>
      </DialogContent>
    </Dialog>
  );
}
