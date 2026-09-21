import {
  createRootRouteWithContext,
  HeadContent,
  Outlet,
  Scripts,
} from "@tanstack/react-router";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import App from "../app";
import css from "../styles.css?url";
export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()(
  {
    ssr: false,
    head: () => ({
      meta: [
        { charSet: "utf-8" },
        { name: "viewport", content: "width=device-width,initial-scale=1" },
        { title: "dimsum · DNS administration" },
      ],
      links: [{ rel: "stylesheet", href: css }],
    }),
    shellComponent: Document,
    component: Root,
  },
);
function Root() {
  const { queryClient } = Route.useRouteContext();
  return (
    <QueryClientProvider client={queryClient}>
      <App />
      <Outlet />
    </QueryClientProvider>
  );
}
function Document({ children }: { children: ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <head>
        <HeadContent />
      </head>
      <body>
        <div id="root">{children}</div>
        <Scripts />
      </body>
    </html>
  );
}
