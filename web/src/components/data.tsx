import { useMemo, type ReactNode } from "react";
import {
  createColumnHelper,
  tableFeatures,
  useTable,
} from "@tanstack/react-table";
import { APIError, text, type Row } from "@/lib/api";
import { Button } from "./ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./ui/table";

export function ErrorNotice({
  error,
  retry,
}: {
  error: Error;
  retry?: () => void;
}) {
  const conflict = error instanceof APIError && error.status === 409;
  return (
    <div
      className="my-3 space-y-2 rounded-md border border-border border-l-[3px] border-l-destructive bg-muted px-4 py-3 text-xs wrap-anywhere [&_pre]:whitespace-pre-wrap"
      role="alert"
    >
      <strong>
        {conflict
          ? "Settings changed since you opened this form"
          : "Couldn't complete the request"}
      </strong>
      <p>
        {conflict
          ? "Your changes haven't been saved. Reload the current settings and try again."
          : error.message}
      </p>
      {error instanceof APIError && (!!error.requestID || !!error.fields) && (
        <details>
          <summary>Technical details</summary>
          {error.fields != null && (
            <pre>{JSON.stringify(error.fields, null, 2)}</pre>
          )}
          {error.requestID && <small>Request {error.requestID}</small>}
        </details>
      )}
      {retry && (
        <Button variant="outline" onClick={retry}>
          Retry
        </Button>
      )}
    </div>
  );
}

export function Resource({
  state,
  children,
  retry,
}: {
  state: { loading: boolean; error?: Error; data?: unknown };
  children: ReactNode;
  retry?: () => void;
}) {
  if (state.loading && !state.data)
    return (
      <div
        className="my-4 rounded-lg bg-muted p-8 text-center text-muted-foreground"
        role="status"
      >
        Loading…
      </div>
    );
  if (state.error && !state.data)
    return <ErrorNotice error={state.error} retry={retry} />;
  return (
    <>
      {state.error && (
        <div
          className="my-3 rounded-md border border-border bg-muted px-4 py-3 text-xs"
          role="status"
        >
          Unable to refresh. Showing the last available data.
        </div>
      )}
      {children}
    </>
  );
}

export type Column = {
  key: string;
  label: string;
  render?: (row: Row) => ReactNode;
  width?: number | string;
  align?: "left" | "right";
  hidden?: boolean;
};
const features = tableFeatures({});
const helper = createColumnHelper<typeof features, Row>();
export function DataTable({
  items,
  columns,
  empty = "No results for this selection.",
}: {
  items: Row[];
  columns: Column[];
  empty?: string;
}) {
  const visible = columns.filter((c) => !c.hidden);
  const definitions = useMemo(
    () =>
      columns
        .filter((c) => !c.hidden)
        .map((c) =>
          helper.accessor((r) => r[c.key], {
            id: c.key,
            header: c.label,
            cell: ({ row }) =>
              c.render ? c.render(row.original) : text(row.original[c.key]),
          }),
        ),
    [columns],
  );
  const table = useTable({
    features,
    columns: definitions,
    data: items,
    getRowId: (r, i) => text(r.id ?? r.address ?? r.name ?? i),
  });
  return (
    <Table className={visible.length > 5 ? "min-w-190" : undefined}>
      <TableHeader>
        {table.getHeaderGroups().map((group) => (
          <TableRow key={group.id}>
            {group.headers.map((header, i) => (
              <TableHead
                key={header.id}
                className="bg-muted px-4 text-xs"
                style={{
                  width: visible[i]?.width,
                  textAlign: visible[i]?.align,
                }}
              >
                <table.FlexRender header={header} />
              </TableHead>
            ))}
          </TableRow>
        ))}
      </TableHeader>
      <TableBody>
        {items.length ? (
          table.getRowModel().rows.map((row) => (
            <TableRow key={row.id}>
              {row.getAllCells().map((cell, i) => (
                <TableCell
                  key={cell.id}
                  className="max-w-105 px-4 py-3 text-base leading-normal whitespace-nowrap tabular-nums"
                  style={{ textAlign: visible[i]?.align }}
                  title={
                    typeof row.original[visible[i]?.key] === "string"
                      ? String(row.original[visible[i].key])
                      : undefined
                  }
                >
                  <table.FlexRender cell={cell} />
                </TableCell>
              ))}
            </TableRow>
          ))
        ) : (
          <TableRow>
            <TableCell
              colSpan={visible.length}
              className="p-9 text-center text-muted-foreground"
            >
              {empty}
            </TableCell>
          </TableRow>
        )}
      </TableBody>
    </Table>
  );
}

export function Details({ value }: { value: unknown }) {
  return (
    <dl className="text-base wrap-anywhere [&>div]:grid [&>div]:grid-cols-[minmax(110px,35%)_1fr] [&>div]:gap-3 [&>div]:border-b [&>div]:border-border [&>div]:py-3 [&_dt]:text-muted-foreground [&_dt]:capitalize [&_dd]:whitespace-pre-wrap">
      {Object.entries(value && typeof value === "object" ? value : {}).map(
        ([key, v]) => (
          <div key={key}>
            <dt>{key.replaceAll("_", " ")}</dt>
            <dd>{text(v)}</dd>
          </div>
        ),
      )}
    </dl>
  );
}
