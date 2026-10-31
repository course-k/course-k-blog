export type AdminView = "new" | "articles" | "edit" | "settings";
export type AdminSearch = { view: AdminView; id?: string };

const views: AdminView[] = ["new", "articles", "edit", "settings"];

/** /admin?view=…&id=…; unknown views become "new", and an edit keeps whatever
 * ID was given so a bad one is reported instead of silently replaced. */
export function validateAdminSearch(search: Record<string, unknown>): AdminSearch {
  const view = views.includes(search.view as AdminView) ? (search.view as AdminView) : "new";
  if (view === "edit") return { view, id: typeof search.id === "string" ? search.id : "" };
  return { view };
}

export function validArticleId(id: string | undefined): id is string {
  return typeof id === "string" && /^[A-Za-z0-9_-]{1,100}$/.test(id);
}
