import { createFileRoute } from "@tanstack/react-router";
import { AdminPage } from "../components/AdminPage";

export const Route = createFileRoute("/admin")({
  head: () => ({
    meta: [
      { title: "記事管理 — course_k" },
      { name: "robots", content: "noindex, nofollow" },
    ],
  }),
  component: AdminPage,
});
