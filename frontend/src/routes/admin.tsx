import { createFileRoute } from "@tanstack/react-router";
import { AdminPage } from "../components/AdminPage";
import { validateAdminSearch } from "../features/editor/adminSearch";

export const Route = createFileRoute("/admin")({
  validateSearch: validateAdminSearch,
  head: () => ({
    meta: [
      { title: "記事管理 — course_k" },
      { name: "robots", content: "noindex, nofollow" },
    ],
  }),
  component: Admin,
});

function Admin() {
  return <AdminPage search={Route.useSearch()} />;
}
