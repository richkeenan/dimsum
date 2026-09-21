import type { ReactNode } from "react";
import { APIError, text, type Meta, type Row } from "@/lib/api";
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
    <div className="notice danger" role="alert">
      <strong>
        {conflict
          ? "Configuration changed on disk"
          : "Request could not be completed"}
      </strong>
      <p>
        {conflict
          ? "Your edits have not been saved. Reload the latest revision before trying again."
          : error.message}
      </p>
      {error instanceof APIError && error.fields != null && (
        <pre>{JSON.stringify(error.fields, null, 2)}</pre>
      )}
      {error instanceof APIError && error.requestID && (
        <small>Request {error.requestID}</small>
      )}
      {retry && (
        <Button variant="outline" onClick={retry}>
          Reload
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
  return state.loading ? (
    <div className="loading" role="status">
      Loading from the service…
    </div>
  ) : state.error ? (
    <ErrorNotice error={state.error} retry={retry} />
  ) : (
    <>{children}</>
  );
}
export function Completeness({ meta }: { meta?: Meta }) {
  return (
    <>
      {(meta?.complete === false ||
        meta?.incomplete ||
        !!meta?.gaps?.length) && (
        <div className="notice" role="status">
          Incomplete history. Missing or dropped intervals are excluded; totals
          may be understated.
        </div>
      )}
      {meta?.updated_at && (
        <small className="muted">
          Updated {new Date(meta.updated_at).toLocaleString()}
        </small>
      )}
    </>
  );
}
export type Column = {
  key: string;
  label: string;
  render?: (row: Row) => ReactNode;
};
export function DataTable({
  items,
  columns,
  empty = "No results for this selection.",
}: {
  items: Row[];
  columns: Column[];
  empty?: string;
}) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          {columns.map((c) => (
            <TableHead key={c.key}>{c.label}</TableHead>
          ))}
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.length ? (
          items.map((r, i) => (
            <TableRow key={text(r.id ?? r.address ?? r.name ?? i)}>
              {columns.map((c) => (
                <TableCell key={c.key}>
                  {c.render ? c.render(r) : text(r[c.key])}
                </TableCell>
              ))}
            </TableRow>
          ))
        ) : (
          <TableRow>
            <TableCell colSpan={columns.length} className="empty">
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
    <dl className="details">
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
