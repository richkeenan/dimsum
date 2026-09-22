import { useEffect, useId, useRef, useState } from "react";
import { ChevronDown } from "lucide-react";
import { Input } from "@/components/ui/input";
import { useResource } from "@/lib/hooks";
import type { ClientsResponse } from "@/lib/api";

export function ClientFilter({
  value,
  onChange,
  range,
  refresh,
}: {
  value: string;
  onChange: (value: string) => void;
  range: string;
  refresh: number;
}) {
  const clients = useResource<ClientsResponse>(
    "clients?" + range + "&limit=200",
    refresh,
  );
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [active, setActive] = useState(-1);
  const byAddress = new Map<string, { address: string; name: string }>();
  for (const client of [
    ...(clients.data?.observed?.items ?? []),
    ...(clients.data?.items ?? []),
  ]) {
    byAddress.set(client.address, client);
  }
  const known = [...byAddress.values()].sort((a, b) =>
    (a.name || a.address).localeCompare(b.name || b.address),
  );
  const selected = byAddress.get(value);
  const display = selected?.name ? `${selected.name} (${value})` : value;
  const term = search.trim().toLowerCase();
  const options = known.filter((c) =>
    `${c.name} ${c.address}`.toLowerCase().includes(term),
  );
  const choices = term
    ? options
    : [{ address: "", name: "All clients" }, ...options];
  // A literal address remains usable even when the client inventory is unavailable.
  if (
    term &&
    /^[\da-f:.%]+$/i.test(term) &&
    /[.:]/.test(term) &&
    !byAddress.has(search.trim())
  ) {
    choices.push({ address: search.trim(), name: "Use address" });
  }
  useEffect(() => {
    setOpen(false);
    setSearch("");
    setActive(-1);
  }, [value]);
  useEffect(() => {
    if (open && active >= 0)
      document
        .getElementById(`${id}-${active}`)
        ?.scrollIntoView?.({ block: "nearest" });
  }, [active, open, id]);
  function choose(address: string) {
    onChange(address);
    setOpen(false);
    setSearch("");
    setActive(-1);
  }
  return (
    <div
      className="relative min-w-0"
      onBlur={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget)) {
          setOpen(false);
          setSearch("");
          setActive(-1);
        }
      }}
    >
      <Input
        ref={input}
        role="combobox"
        aria-label="Filter client"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        aria-autocomplete="list"
        aria-activedescendant={
          open && choices[active] ? `${id}-${active}` : undefined
        }
        autoComplete="off"
        placeholder={open ? "Search name or IP…" : "All clients"}
        className="pr-9"
        value={open ? search : display}
        onFocus={() => setOpen(true)}
        onClick={() => setOpen(true)}
        onChange={(e) => {
          setSearch(e.target.value);
          setActive(-1);
          setOpen(true);
        }}
        onKeyDown={(e) => {
          if (e.key === "ArrowDown" || e.key === "ArrowUp") {
            e.preventDefault();
            setOpen(true);
            setActive((i) =>
              e.key === "ArrowDown"
                ? Math.min(i + 1, choices.length - 1)
                : Math.max(i - 1, 0),
            );
          } else if (e.key === "Enter" && open) {
            e.preventDefault();
            const choice =
              choices[active] ??
              (choices.length === 1 ? choices[0] : undefined);
            if (choice) choose(choice.address);
          } else if (e.key === "Escape") {
            e.preventDefault();
            setOpen(false);
            setSearch("");
            setActive(-1);
          }
        }}
      />
      <button
        type="button"
        tabIndex={-1}
        aria-label="Show clients"
        className="absolute inset-y-0 right-0 flex w-9 items-center justify-center text-muted-foreground"
        onMouseDown={(e) => e.preventDefault()}
        onClick={() => {
          input.current?.focus();
          setOpen(!open);
        }}
      >
        <ChevronDown className="size-4" strokeWidth={1.5} />
      </button>
      {open && (
        <div className="absolute top-full z-20 mt-1 w-full min-w-0 rounded-md border border-border bg-background p-1 shadow-md">
          <ul
            id={id}
            role="listbox"
            aria-label="Clients"
            className="max-h-64 overflow-y-auto"
          >
            {choices.map((client, i) => (
              <li
                id={`${id}-${i}`}
                key={client.address}
                role="option"
                aria-selected={client.address === value}
                className={`cursor-pointer rounded-sm px-2.5 py-2 text-sm hover:bg-accent ${active === i ? "bg-accent" : ""}`}
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => choose(client.address)}
              >
                <span className="block truncate">
                  {client.name || client.address}
                </span>
                {client.name && client.address && (
                  <span className="block truncate text-xs text-muted-foreground">
                    {client.address}
                  </span>
                )}
              </li>
            ))}
          </ul>
          {choices.length === 0 && (
            <p className="px-2.5 py-2 text-xs text-muted-foreground">
              {clients.loading
                ? "Loading clients…"
                : "No matching clients. Enter an IP address."}
            </p>
          )}
          {(clients.error || clients.data?.observed_available === false) && (
            <p className="px-2.5 py-2 text-xs text-muted-foreground">
              Client history unavailable. You can enter an IP address.
            </p>
          )}
          {clients.data?.observed?.truncated && (
            <p className="px-2.5 py-2 text-xs text-muted-foreground">
              Showing the first 200 observed clients. Enter an IP for others.
            </p>
          )}
        </div>
      )}
    </div>
  );
}
