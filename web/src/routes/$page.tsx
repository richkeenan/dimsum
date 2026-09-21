import { createFileRoute, notFound } from "@tanstack/react-router";
import { validateView, pages } from "../lib/navigation";
export const Route = createFileRoute("/$page")({
  validateSearch: validateView,
  beforeLoad: ({ params }) => {
    if (!pages.includes(params.page)) throw notFound();
  },
  component: () => null,
});
