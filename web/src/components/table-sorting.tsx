import { useMemo, useState, type ComponentProps } from "react";
import {
  createColumnHelper,
  createSortedRowModel,
  rowSortingFeature,
  tableFeatures,
  useTable,
  type SortingState,
  type OnChangeFn,
} from "@tanstack/react-table";
import { ArrowDown, ArrowUp, ArrowUpDown } from "lucide-react";
import { TableHead } from "./ui/table";

export type SortColumn<T> = {
  key: string;
  label: string;
  hidden?: boolean;
  sortable?: boolean;
  sortValue?: (row: T) => unknown;
  sortType?: "text" | "number" | "datetime" | "address";
  sortDescFirst?: boolean;
};
export type SortingControl = {
  value: SortingState;
  onChange: OnChangeFn<SortingState>;
};

const features = tableFeatures({
  rowSortingFeature,
  sortedRowModel: createSortedRowModel(),
});
const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });
type SortValue = string | number | bigint | boolean | undefined;

function addressKey(value: string): string {
  // URL canonicalizes IPv6 (including embedded IPv4) before expanding ::.
  try {
    if (value.includes(":")) {
      const host = new URL(`http://[${value.replace(/^\[|\]$/g, "")}]`).hostname.slice(1, -1);
      const [left, right] = host.split("::");
      const head = left ? left.split(":") : [];
      const tail = right ? right.split(":") : [];
      const parts =
        right === undefined
          ? head
          : [...head, ...Array(8 - head.length - tail.length).fill("0"), ...tail];
      return `6:${parts.map((part) => part.padStart(4, "0")).join(":")}`;
    }
    if (/^\d+\.\d+\.\d+\.\d+$/.test(value)) {
      return `4:${value
        .split(".")
        .map((part) => part.padStart(3, "0"))
        .join(".")}`;
    }
  } catch {
    // Non-address labels retain a predictable text order.
  }
  return `text:${value}`;
}

function sortValue(value: unknown, type: SortColumn<unknown>["sortType"]): SortValue {
  if (value === null || value === undefined || value === "") return undefined;
  if (type === "datetime") {
    const raw = String(value);
    const time = new Date(raw).getTime();
    if (!Number.isFinite(time)) return undefined;
    // The API emits microseconds (and some timestamps nanoseconds). Date handles
    // timezone offsets, but drops fractional precision beyond milliseconds.
    const fraction = raw.match(/\.(\d+)(?:Z|[+-]\d{2}:\d{2})$/i)?.[1] ?? "";
    return BigInt(time) * BigInt(1_000_000) + BigInt(fraction.padEnd(9, "0").slice(3, 9));
  }
  if (type === "number") {
    if (typeof value === "bigint") return value;
    const raw = String(value);
    if (/^-?\d+$/.test(raw)) return BigInt(raw);
    const number = Number(raw);
    return Number.isFinite(number) ? number : undefined;
  }
  if (type === "address") return addressKey(String(value));
  return typeof value === "string" ||
    typeof value === "number" ||
    typeof value === "bigint" ||
    typeof value === "boolean"
    ? value
    : undefined;
}

export function useSortableTable<T extends object>({
  items,
  columns,
  initialSorting = [],
  sorting: controlledSorting,
  getRowId,
}: {
  items: T[];
  columns: SortColumn<T>[];
  initialSorting?: SortingState;
  sorting?: SortingControl;
  getRowId: (row: T, index: number) => string;
}) {
  const [sorting, setSorting] = useState<SortingState>(initialSorting);
  const definitions = useMemo(() => {
    const helper = createColumnHelper<typeof features, T>();
    return columns
      .filter((c) => !c.hidden)
      .map((c) =>
        helper.accessor(
          (row): unknown =>
            sortValue(
              c.sortValue ? c.sortValue(row) : (row as Record<string, unknown>)[c.key],
              c.sortType,
            ),
          {
            id: c.key,
            header: c.label,
            enableSorting: c.sortable !== false,
            sortUndefined: "last",
            sortDescFirst:
              c.sortDescFirst ?? (c.sortType === "number" || c.sortType === "datetime"),
            sortFn: (a, b, id) => {
              const left = a.getValue<SortValue>(id);
              const right = b.getValue<SortValue>(id);
              if (typeof left === "string" && typeof right === "string") {
                // Address keys contain padded hexadecimal; compare them lexically.
                if (
                  c.sortType === "address" &&
                  !(left.startsWith("text:") && right.startsWith("text:"))
                )
                  return left < right ? -1 : left > right ? 1 : 0;
                return collator.compare(left, right);
              }
              return left! < right! ? -1 : left! > right! ? 1 : 0;
            },
          },
        ),
      );
  }, [columns]);
  return useTable({
    features,
    columns: definitions,
    data: items,
    getRowId,
    state: {
      sorting: (controlledSorting?.value ?? sorting).filter((sort) =>
        columns.some((c) => c.key === sort.id && !c.hidden && c.sortable !== false),
      ),
    },
    onSortingChange: controlledSorting?.onChange ?? setSorting,
    enableMultiSort: false,
    enableSortingRemoval: false,
  });
}

export function SortableHead({
  column,
  sorted,
  label,
  align,
  ...props
}: ComponentProps<typeof TableHead> & {
  column: {
    getCanSort: () => boolean;
    toggleSorting: () => void;
  };
  label: string;
  sorted: false | "asc" | "desc";
  align?: "left" | "right";
}) {
  const Icon = sorted === "asc" ? ArrowUp : sorted === "desc" ? ArrowDown : ArrowUpDown;
  return (
    <TableHead
      {...props}
      aria-sort={sorted ? (sorted === "asc" ? "ascending" : "descending") : undefined}
    >
      {column.getCanSort() ? (
        <button
          type="button"
          onClick={() => column.toggleSorting()}
          className={`flex min-h-10 w-full cursor-pointer items-center gap-1 rounded-sm text-inherit hover:text-primary focus-visible:outline-2 focus-visible:outline-ring ${align === "right" ? "justify-end" : "justify-start"}`}
        >
          {label}
          <Icon
            aria-hidden="true"
            className={`size-3.5 shrink-0 ${sorted ? "text-primary" : "text-muted-foreground"}`}
          />
        </button>
      ) : (
        label
      )}
    </TableHead>
  );
}
