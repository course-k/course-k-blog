import { createFileRoute } from "@tanstack/react-router";
import { Admin } from "../components/Editor";

export const Route = createFileRoute("/admin")({
  head: () => ({
    meta: [
      { title: "記事管理 — course_k" },
      { name: "robots", content: "noindex, nofollow" },
    ],
  }),
  component: Admin,
});
