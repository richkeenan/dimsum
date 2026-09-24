import { useId, useState } from "react";
import { ChevronRight } from "lucide-react";
import { DataTable, type Column } from "@/components/data";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { Row } from "@/lib/api";
import type { components } from "@/lib/openapi";

type Category = NonNullable<components["schemas"]["CatalogItem"]["category"]>;
type Group = { id: Category | "custom"; label: string; badge: string; className: string };

const groups: Group[] = [
  {
    id: "ads-trackers",
    label: "Ads & Trackers",
    badge: "Ads & trackers",
    className: "bg-blue-100 text-blue-800 dark:bg-blue-950 dark:text-blue-200",
  },
  {
    id: "security",
    label: "Security & Scams",
    badge: "Security & scams",
    className: "bg-teal-100 text-teal-800 dark:bg-teal-950 dark:text-teal-200",
  },
  {
    id: "social-gambling",
    label: "Social & Gambling",
    badge: "Social & gambling",
    className: "bg-purple-100 text-purple-800 dark:bg-purple-950 dark:text-purple-200",
  },
  { id: "parental-control", label: "Adult Content", badge: "Adult content", className: "" },
  {
    id: "compatibility",
    label: "Compatibility",
    badge: "Compatibility",
    className: "bg-amber-100 text-amber-900 dark:bg-amber-950 dark:text-amber-200",
  },
  {
    id: "custom",
    label: "Custom Lists",
    badge: "Custom",
    className: "bg-secondary text-secondary-foreground",
  },
];
const storageKey = "dimsum-list-groups";

function groupFor(category: unknown) {
  return groups.find((group) => group.id === category) ?? groups[groups.length - 1]!;
}

export function ListCategoryBadge({
  category,
  heading = false,
}: {
  category: unknown;
  heading?: boolean;
}) {
  const group = groupFor(category);
  return (
    <Badge
      variant={group.id === "parental-control" ? "destructive" : "secondary"}
      className={group.className}
    >
      {heading ? group.label : group.badge}
    </Badge>
  );
}

function savedGroups(): string[] {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(storageKey) ?? "[]");
    return Array.isArray(value) ? value.filter((id): id is string => typeof id === "string") : [];
  } catch {
    return [];
  }
}

export function GroupedListTable({
  items,
  columns,
  pending,
  empty,
}: {
  items: Row[];
  columns: Column[];
  pending?: { id: unknown; enabled: boolean };
  empty?: string;
}) {
  const id = useId();
  const [expanded, setExpanded] = useState(savedGroups);
  const visible = groups
    .map((group) => ({
      ...group,
      items: items.filter((row) => groupFor(row.category).id === group.id),
    }))
    .filter((group) => group.items.length);
  function update(next: string[]) {
    setExpanded(next);
    try {
      localStorage.setItem(storageKey, JSON.stringify(next));
    } catch {
      // The controls still work when browser storage is disabled.
    }
  }
  if (!items.length) return <DataTable items={items} columns={columns} empty={empty} />;
  const allExpanded = visible.every((group) => expanded.includes(group.id));
  return (
    <div>
      <div className="flex justify-end px-4 pb-2">
        <Button
          size="sm"
          variant="ghost"
          onClick={() => update(allExpanded ? [] : groups.map((group) => group.id))}
        >
          {allExpanded ? "Collapse all" : "Expand all"}
        </Button>
      </div>
      {visible.map((group) => {
        const open = expanded.includes(group.id);
        const used = group.items.filter((row) =>
          pending && pending.id === row.id
            ? pending.enabled
            : (row.default_apply ?? row.enabled) === true,
        ).length;
        const issues = group.items.filter(
          (row) => row.enabled === true && !!(row.source as Row | undefined)?.error,
        ).length;
        const headingID = `${id}-${group.id}-heading`;
        const panelID = `${id}-${group.id}-panel`;
        return (
          <section key={group.id} className="min-w-0 border-t border-border">
            <h3>
              <button
                id={headingID}
                type="button"
                aria-expanded={open}
                aria-controls={panelID}
                onClick={() =>
                  update(
                    open ? expanded.filter((key) => key !== group.id) : [...expanded, group.id],
                  )
                }
                className="flex min-h-14 w-full cursor-pointer items-center gap-3 px-4 py-3 text-left hover:bg-muted/50 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring"
              >
                <ChevronRight
                  aria-hidden="true"
                  className={`size-4 shrink-0 text-muted-foreground ${open ? "rotate-90" : ""}`}
                />
                <span className="flex flex-1 flex-wrap items-center gap-x-3 gap-y-2">
                  <ListCategoryBadge category={group.id} heading />
                  <span className="text-xs font-normal text-muted-foreground tabular-nums">
                    {group.items.length} {group.items.length === 1 ? "list" : "lists"} · {used} used
                    by default
                  </span>
                  {issues > 0 && (
                    <Badge variant="destructive">
                      {issues} {issues === 1 ? "issue" : "issues"}
                    </Badge>
                  )}
                </span>
              </button>
            </h3>
            <div id={panelID} role="region" aria-labelledby={headingID} hidden={!open}>
              {open && <DataTable items={group.items} columns={columns} />}
            </div>
          </section>
        );
      })}
    </div>
  );
}
