// Day-by-day projection of a campaign that does not exist yet: first emails,
// follow-ups and the warmup running alongside, stacked per day against what
// the pool can send for the campaign. Plain divs; one axis, one unit.

import React from "react";
import type { CampaignEstimateDay } from "@/lib/api/client/app/campaigns/estimateCampaign";
import { fmtDay } from "./draft";
import { cn } from "@/lib/utils";

const FIRST = "bg-[#0369a1]"; // sky-700
const FOLLOW = "bg-[#38bdf8]"; // sky-400
const WARMUP = "bg-[#fcd34d]"; // amber-300

type Bar = CampaignEstimateDay & { until?: string };

// Days summed into bars of `size` consecutive days.
function bucket(days: CampaignEstimateDay[], size: number): Bar[] {
    if (size <= 1) return days;
    const out: Bar[] = [];
    for (let i = 0; i < days.length; i += size) {
        const chunk = days.slice(i, i + size);
        out.push({
            date: chunk[0].date,
            until: chunk[chunk.length - 1].date,
            sending_day: chunk.some((d) => d.sending_day),
            capacity: chunk.reduce((n, d) => n + d.capacity, 0),
            sends: chunk.reduce((n, d) => n + d.sends, 0),
            first_emails: chunk.reduce((n, d) => n + d.first_emails, 0),
            follow_ups: chunk.reduce((n, d) => n + d.follow_ups, 0),
            warmup: chunk.reduce((n, d) => n + d.warmup, 0),
        });
    }
    return out;
}

export default function EstimateTimeline({
    days,
    height = 96,
    maxBars = 92,
    showFollowUps,
    showWarmup = false,
    mailboxes = 0,
    className,
}: {
    days: CampaignEstimateDay[];
    height?: number;
    // Longer projections are summed into weeks (or more) to stay readable.
    maxBars?: number;
    showFollowUps: boolean;
    // Stack the warmup on top, so a bar is everything the pool sends that day.
    showWarmup?: boolean;
    // Pool size, for the per-mailbox figure in the tooltip.
    mailboxes?: number;
    className?: string;
}) {
    const [hover, setHover] = React.useState<number | null>(null);
    // The hovered bar's centre and the row's width, so the tooltip sits on the bar.
    const [anchor, setAnchor] = React.useState<{ x: number; width: number } | null>(null);
    const { shown, size } = React.useMemo(() => {
        // Trailing idle days after the last send add nothing.
        let last = -1;
        for (let i = days.length - 1; i >= 0; i--) {
            if (days[i].sends > 0) {
                last = i;
                break;
            }
        }
        const active = last >= 0 ? days.slice(0, last + 1) : [];
        const size = active.length <= maxBars ? 1 : 7 * Math.ceil(active.length / (7 * maxBars));
        return { shown: bucket(active, size), size };
    }, [days, maxBars]);
    const warm = (d: Bar) => (showWarmup ? d.warmup : 0);
    const peak = Math.max(1, ...shown.map((d) => Math.max(d.sends + warm(d), d.capacity)));
    if (shown.length === 0) return null;

    const h = hover !== null ? shown[hover] : null;
    const labelEvery = Math.max(1, Math.ceil(shown.length / 5));
    const unit = size === 1 ? "day" : size === 7 ? "week" : `${size} days`;

    return (
        <div
            className={cn("relative", className)}
            onMouseLeave={() => {
                setHover(null);
                setAnchor(null);
            }}
        >
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[10.5px] text-slate-500 mb-2">
                <span className="inline-flex items-center gap-1.5">
                    <span className={cn("size-2 rounded-sm", FIRST)} />
                    First emails
                </span>
                {showFollowUps && (
                    <span className="inline-flex items-center gap-1.5">
                        <span className={cn("size-2 rounded-sm", FOLLOW)} />
                        Follow-ups
                    </span>
                )}
                {showWarmup && (
                    <span className="inline-flex items-center gap-1.5">
                        <span className={cn("size-2 rounded-sm", WARMUP)} />
                        Warmup
                    </span>
                )}
                <span className="inline-flex items-center gap-1.5">
                    <span className="size-2 rounded-sm bg-slate-100 ring-1 ring-inset ring-slate-200" />
                    Campaign capacity
                </span>
                <span className="ml-auto text-slate-400">Each bar is one {unit}</span>
            </div>
            <div
                className="flex items-end gap-[2px] border-b border-slate-200"
                style={{ height }}
                role="img"
                aria-label={`Projected sends, one bar per ${unit}, ${shown.length} bars`}
            >
                {shown.map((d, i) => {
                    const cap = (d.capacity / peak) * 100;
                    const parts = [
                        { n: warm(d), cls: WARMUP },
                        { n: d.follow_ups, cls: FOLLOW },
                        { n: d.first_emails, cls: FIRST },
                    ].filter((p) => p.n > 0);
                    const total = parts.reduce((n, p) => n + p.n, 0);
                    return (
                        <div
                            key={d.date}
                            onMouseEnter={(ev) => {
                                const bar = ev.currentTarget;
                                setHover(i);
                                setAnchor({ x: bar.offsetLeft + bar.offsetWidth / 2, width: bar.parentElement?.offsetWidth ?? 0 });
                            }}
                            className="relative flex-1 max-w-6 h-full flex flex-col justify-end cursor-default"
                        >
                            {d.sending_day && d.capacity > 0 && (
                                <div
                                    className={cn(
                                        "absolute inset-x-0 bottom-0 rounded-t-[4px] transition-colors",
                                        hover === i ? "bg-slate-200/70" : "bg-slate-100",
                                    )}
                                    style={{ height: `${cap}%` }}
                                />
                            )}
                            <div className="relative flex flex-col justify-end gap-[2px]" style={{ height: `${Math.min(100, (total / peak) * 100)}%` }}>
                                {parts.map((p, k) => (
                                    <div key={p.cls} className={cn("w-full", p.cls, k === 0 && "rounded-t-[4px]")} style={{ flexBasis: `${(p.n / total) * 100}%` }} />
                                ))}
                            </div>
                        </div>
                    );
                })}
            </div>
            <div className="flex gap-[2px] mt-1 text-[9.5px] text-slate-400 tabular-nums">
                {shown.map((d, i) => (
                    <div key={d.date} className="flex-1 max-w-6 relative h-3">
                        {i % labelEvery === 0 && <span className="absolute left-0 whitespace-nowrap">{fmtDay(d.date)}</span>}
                    </div>
                ))}
            </div>
            {h && anchor && (
                <div
                    className="pointer-events-none absolute z-10 top-0 rounded-md border border-slate-200 bg-white px-2.5 py-2 shadow-[0_8px_24px_-8px_rgba(15,23,42,0.18)] text-[11px] min-w-[170px]"
                    style={{
                        left: anchor.x,
                        transform: `translate(${anchor.x > anchor.width / 2 ? "-100%" : "0"}, -100%)`,
                    }}
                >
                    <p className="text-slate-900 font-medium mb-1">
                        {hover === 0 && size === 1 ? "Day 1, " : ""}
                        {fmtDay(h.date)}
                        {h.until && h.until !== h.date ? ` to ${fmtDay(h.until)}` : ""}
                    </p>
                    <div className="space-y-0.5 tabular-nums">
                        {!h.sending_day ? (
                            <p className="text-slate-500">{h.until ? "No campaign sending days" : "No campaign sends on this day"}</p>
                        ) : (
                            <>
                                <Row swatch={FIRST} label="First emails" value={h.first_emails} />
                                {showFollowUps && <Row swatch={FOLLOW} label="Follow-ups" value={h.follow_ups} />}
                            </>
                        )}
                        {warm(h) > 0 && <Row swatch={WARMUP} label="Warmup" value={warm(h)} />}
                    </div>
                    <div className="mt-1 pt-1 border-t border-slate-100 space-y-0.5 tabular-nums">
                        <Row label="Sent in total" value={h.sends + warm(h)} />
                        {mailboxes > 0 && size === 1 && h.sends + warm(h) > 0 && (
                            <Row label="Per mailbox, about" value={Math.round((h.sends + warm(h)) / mailboxes)} muted />
                        )}
                        {h.sending_day && <Row label="Campaign capacity" value={h.capacity} muted />}
                    </div>
                </div>
            )}
        </div>
    );
}

function Row({ swatch, label, value, muted }: { swatch?: string; label: string; value: number; muted?: boolean }) {
    return (
        <p className="flex items-center gap-1.5">
            {swatch ? <span className={cn("size-2 rounded-sm", swatch)} /> : <span className="size-2" />}
            <span className={muted ? "text-slate-400" : "text-slate-600"}>{label}</span>
            <span className={cn("ml-auto", muted ? "text-slate-400" : "text-slate-900 font-medium")}>{value.toLocaleString()}</span>
        </p>
    );
}
