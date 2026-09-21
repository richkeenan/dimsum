import { createFileRoute } from "@tanstack/react-router";
import { validateView } from "../lib/navigation";
export const Route = createFileRoute("/")({
  validateSearch: validateView,
  component: () => null,
});
