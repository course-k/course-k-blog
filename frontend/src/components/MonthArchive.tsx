import { Link } from "@tanstack/react-router";
import type { ArchiveMonth } from "../lib/types";

export function monthLabel(month: string) {
  const [year, number] = month.split("-");
  return `${year}年${Number(number)}月`;
}

export function MonthArchive({ months, selected }: { months: ArchiveMonth[]; selected?: string }) {
  const years = [...new Set(months.map(({ month }) => month.slice(0, 4)))];
  return (
    <details className="month-archive" open={Boolean(selected)}>
      <summary>月別に探す</summary>
      {months.length === 0 ? (
        <p className="archive-empty">公開された記事がある月が、ここに並びます。</p>
      ) : (
        <nav className="archive-years" aria-label="月別アーカイブ">
          {years.map((year, index) => (
            <details key={year} open={selected ? selected.startsWith(year) : index === 0}>
              <summary>{year}年</summary>
              <ul className="archive-months">
                {months.filter(({ month }) => month.startsWith(year)).map(({ month, count }) => (
                  <li key={month}>
                    <Link to="/" search={{ page: 1, month }} data-selected={selected === month || undefined}>
                      {Number(month.slice(5))}月 <span>({count})</span>
                    </Link>
                  </li>
                ))}
              </ul>
            </details>
          ))}
        </nav>
      )}
    </details>
  );
}
