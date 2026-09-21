import { useRef, useState } from "react";
import { api } from "@/lib/api";
import { useResource } from "@/lib/hooks";
import { ErrorNotice } from "@/components/data";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

interface AgentToken {
  id: string;
  name: string;
  created_at: string;
}

interface CreatedToken extends AgentToken {
  token: string;
}

export function AgentAccess() {
  const [tick, setTick] = useState(0);
  const state = useResource<{ items: AgentToken[] }>("tokens", tick);
  const [name, setName] = useState("");
  const [created, setCreated] = useState<CreatedToken>();
  const [pending, setPending] = useState<string>();
  const [error, setError] = useState<Error>();
  const [copyStatus, setCopyStatus] = useState("");
  const url = window.location.origin + "/mcp";
  const tokenField = useRef<HTMLInputElement>(null);
  const connectionField = useRef<HTMLTextAreaElement>(null);

  async function copy(field: HTMLInputElement | HTMLTextAreaElement | null) {
    if (!field) return;
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(field.value);
        setCopyStatus("Copied to clipboard.");
        return;
      }
    } catch {
      // Fall back to selecting the visible field when clipboard access is denied.
    }
    field.focus();
    field.select();
    try {
      if (document.execCommand?.("copy")) {
        setCopyStatus("Copied to clipboard.");
        return;
      }
    } catch {
      // Keep the field selected for manual copying.
    }
    setCopyStatus("Copy was blocked. Copy the selected text manually.");
  }

  return (
    <section
      className="mb-5 min-w-0 overflow-hidden rounded-lg border border-border bg-background p-5"
      aria-labelledby="agent-access-heading"
    >
      <h2 id="agent-access-heading" className="mb-3 text-sm font-medium">
        Agent access
      </h2>
      <p className="mb-4 text-xs text-muted-foreground">
        Tokens grant full administrator access and remain valid until revoked.
        Each token is shown only once, when created. Your existing password
        stays unchanged.
      </p>
      <label className="mb-4 flex min-w-0 flex-col gap-1.5 text-xs font-normal">
        MCP URL
        <Input
          readOnly
          value={url}
          onFocus={(event) => event.currentTarget.select()}
          className="font-mono"
        />
      </label>

      <form
        className="mt-5 flex flex-wrap items-end gap-3"
        onSubmit={async (event) => {
          event.preventDefault();
          if (pending || created || !name.trim()) return;
          setPending("create");
          setError(undefined);
          setCopyStatus("");
          try {
            const result = await api.send<CreatedToken>("tokens", "POST", {
              name: name.trim(),
            });
            setCreated(result);
            setName("");
            setTick((value) => value + 1);
          } catch (cause) {
            setError(cause as Error);
          } finally {
            setPending(undefined);
          }
        }}
      >
        <label className="flex min-w-0 flex-1 basis-48 flex-col gap-1.5 text-xs font-normal">
          Token name
          <Input
            required
            maxLength={80}
            autoComplete="off"
            placeholder="e.g. Desktop assistant"
            value={name}
            disabled={!!pending || !!created}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <Button type="submit" disabled={!!pending || !!created || !name.trim()}>
          {pending === "create" ? "Creating…" : "Create token"}
        </Button>
      </form>

      {created && (
        <div className="mt-4 min-w-0 rounded-md border border-border bg-muted p-4">
          <h3 className="text-xs font-medium">
            Save your token for {created.name}
          </h3>
          <p className="mt-2 text-xs text-muted-foreground">
            Copy it now. Done or leaving this page hides the secret permanently.
          </p>
          <label className="mt-3 flex min-w-0 flex-col gap-1.5 text-xs font-normal">
            New token
            <Input
              ref={tokenField}
              readOnly
              autoComplete="off"
              value={created.token}
              onFocus={(event) => event.currentTarget.select()}
              className="font-mono"
            />
          </label>
          <label className="mt-3 flex min-w-0 flex-col gap-1.5 text-xs font-normal">
            Connection fields
            <textarea
              ref={connectionField}
              readOnly
              rows={3}
              value={`URL: ${url}\nAuthorization: Bearer ${created.token}`}
              onFocus={(event) => event.currentTarget.select()}
              className="w-full min-w-0 resize-y rounded-md border border-input bg-background px-3 py-2 font-mono text-xs text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
            />
          </label>
          <div className="mt-3 flex flex-wrap gap-2">
            <Button type="button" onClick={() => void copy(tokenField.current)}>
              Copy token
            </Button>
            <Button
              type="button"
              variant="outline"
              onClick={() =>
                void copy(connectionField.current)
              }
            >
              Copy connection fields
            </Button>
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                setCreated(undefined);
                setCopyStatus("");
              }}
            >
              Done
            </Button>
          </div>
          {copyStatus && (
            <p role="status" className="mt-3 text-xs text-muted-foreground">
              {copyStatus}
            </p>
          )}
        </div>
      )}
      {error && <ErrorNotice error={error} />}
      <div className="mt-5">
        <h3 className="mb-3 text-xs font-medium">Existing tokens</h3>
        {state.loading && (
          <p role="status" className="text-xs text-muted-foreground">
            Loading tokens…
          </p>
        )}
        {state.error && (
          <ErrorNotice
            error={state.error}
            retry={() => setTick((value) => value + 1)}
          />
        )}
        {state.data?.items.length === 0 && (
          <p className="text-xs text-muted-foreground">No agent tokens yet.</p>
        )}
        <ul className="divide-y divide-border">
          {state.data?.items.map((token) => (
            <li
              key={token.id}
              className="flex min-w-0 flex-wrap items-center justify-between gap-3 py-3"
            >
              <div className="min-w-0 text-xs [overflow-wrap:anywhere]">
                <p className="font-normal">{token.name}</p>
                <p className="mt-1 text-muted-foreground">
                  Created{" "}
                  <time dateTime={token.created_at}>
                    {new Date(token.created_at).toLocaleString()}
                  </time>
                </p>
              </div>
              <Button
                type="button"
                variant="outline"
                aria-label={`Revoke ${token.name}`}
                disabled={!!pending}
                onClick={async () => {
                  if (pending) return;
                  setPending(token.id);
                  setError(undefined);
                  try {
                    await api.send<{ revoked: boolean }>(
                      `tokens/${encodeURIComponent(token.id)}`,
                      "DELETE",
                    );
                    if (created?.id === token.id) {
                      setCreated(undefined);
                      setCopyStatus("");
                    }
                    setTick((value) => value + 1);
                  } catch (cause) {
                    setError(cause as Error);
                  } finally {
                    setPending(undefined);
                  }
                }}
              >
                {pending === token.id ? "Revoking…" : "Revoke"}
              </Button>
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}
