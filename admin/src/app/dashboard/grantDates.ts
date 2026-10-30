/** A date input gives "YYYY-MM-DD"; a grant runs to the end of that day in the operator's own zone. */
export function endOfLocalDay(day: string): string {
    const [y, m, d] = day.split("-").map(Number);
    return new Date(y, m - 1, d, 23, 59, 59, 999).toISOString();
}

export function fmtDay(ts?: string | null): string {
    if (!ts) return "—";
    return new Date(ts).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}
