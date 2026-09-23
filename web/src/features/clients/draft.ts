import { useCallback, useRef, useState } from "react";
import { useBlocker } from "@tanstack/react-router";

// A ref clears the guard synchronously after a successful save, before a
// promotion/deletion navigates. State controls the browser's unload protection.
export function usePolicyDraft() {
  const pending = useRef({ policy: false, creation: false });
  const [dirty, setDirty] = useState(false);
  const onDirty = useCallback((value: boolean) => {
    pending.current.policy = value;
    setDirty(value || pending.current.creation);
  }, []);
  const onCreateDirty = useCallback((value: boolean) => {
    pending.current.creation = value;
    setDirty(value || pending.current.policy);
  }, []);
  const confirmLeave = useCallback(
    () =>
      !(pending.current.policy || pending.current.creation) ||
      window.confirm("Discard unsaved policy changes?"),
    [],
  );
  useBlocker({
    shouldBlockFn: () => !confirmLeave(),
    enableBeforeUnload: dirty,
  });
  return { onDirty, onCreateDirty, confirmLeave };
}
