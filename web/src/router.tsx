import { createRouter } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { routeTree } from "./routeTree.gen";
import { parseViewSearch, stringifyViewSearch } from "./lib/navigation";
export function getRouter() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { staleTime: 1000, gcTime: 60_000, refetchOnWindowFocus: true },
    },
  });
  return createRouter({
    routeTree,
    parseSearch: parseViewSearch,
    stringifySearch: stringifyViewSearch,
    context: { queryClient },
    scrollRestoration: true,
    defaultPendingComponent: () => (
      <div className="p-8 text-center text-muted-foreground" role="status">
        Loading dimsum…
      </div>
    ),
  });
}
declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof getRouter>;
  }
}
