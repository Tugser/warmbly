// /events — the live platform event feed. Every realtime event pushed to
// admin:platform lands here as it happens (fed by RealtimeManager through
// the events ring buffer). Purely event-driven: no polling, no refetch.

import { useMemo, useState } from "react";
import { ChevronRight, Pause, Play, Radio, Search, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/layout/PageHeader";
import { EmptyState, StatusBadge, StatusDot } from "@/components/ui/kit";
import { TONE_DOT, TONE_TEXT, type Tone } from "@/lib/tones";
import { cn } from "@/lib/utils";
import {
    MAX_EVENTS,
    clearEvents,
    setPaused,
    useLiveEvents,
    useRealtimeStatus,
    type LiveEvent,
} from "@/lib/realtime/eventsStore";
import { getRefusal, refusalMessage, type RealtimeStatus } from "@/lib/realtime/socket";

// ---------------------------------------------------------------- families

type FamilyId = "all" | "emails" | "campaigns" | "accounts" | "audit" | "workers" | "other";

const FAMILY_MATCHERS: { id: Exclude<FamilyId, "all" | "other">; match: (n: string) => boolean }[] = [
    { id: "emails", match: (n) => n.includes("EMAIL") },
    { id: "campaigns", match: (n) => n.includes("CAMPAIGN") },
    { id: "accounts", match: (n) => n.includes("ACCOUNT") || n.includes("WARMUP") },
    { id: "audit", match: (n) => n.includes("AUDIT") },
    { id: "workers", match: (n) => n.includes("WORKER") },
];

const FAMILY_CHIPS: { id: FamilyId; label: string }[] = [
    { id: "all", label: "All" },
    { id: "emails", label: "Emails" },
    { id: "campaigns", label: "Campaigns" },
    { id: "accounts", label: "Accounts & Warmup" },
    { id: "audit", label: "Audit" },
    { id: "workers", label: "Workers" },
    { id: "other", label: "Other" },
];

function inFamily(name: string, family: FamilyId): boolean {
    if (family === "all") return true;
    const upper = name.toUpperCase();
    if (family === "other") return FAMILY_MATCHERS.every((f) => !f.match(upper));
    return FAMILY_MATCHERS.find((f) => f.id === family)!.match(upper);
}

// Tone by family; errors win over everything.
function eventTone(name: string): Tone {
    const n = name.toUpperCase();
    if (n.includes("ERROR") || n.includes("FAILED")) return "danger";
    if (n.includes("ACCOUNT") || n.includes("WARMUP")) return "warning";
    if (n.includes("CAMPAIGN")) return "strong";
    if (n.includes("EMAIL")) return "info";
    return "neutral";
}

// ------------------------------------------------------------------ pieces

const STATUS_META: Record<RealtimeStatus, { label: string; tone: Tone; pulse: boolean }> = {
    connected: { label: "Connected", tone: "success", pulse: true },
    connecting: { label: "Connecting", tone: "warning", pulse: true },
    disconnected: { label: "Disconnected", tone: "danger", pulse: false },
    refused: { label: "Not available", tone: "danger", pulse: false },
};

function ConnectionStatus({ status }: { status: RealtimeStatus }) {
    const meta = STATUS_META[status];
    return (
        <StatusDot tone={meta.tone} pulse={meta.pulse} className={cn("text-xs font-medium", TONE_TEXT[meta.tone])}>
            {meta.label}
        </StatusDot>
    );
}

const ID_FIELDS = ["org_id", "user_id", "campaign_id", "email_account_id"] as const;

function idSummary(payload: Record<string, unknown>): { key: string; value: string }[] {
    const out: { key: string; value: string }[] = [];
    for (const key of ID_FIELDS) {
        const v = payload[key];
        if (typeof v === "string" && v.length > 0) {
            out.push({ key: key.replace(/_id$/, ""), value: v.slice(0, 8) });
        }
    }
    return out;
}

function matchesQuery(ev: LiveEvent, q: string): boolean {
    if (ev.name.toLowerCase().includes(q)) return true;
    for (const value of Object.values(ev.payload)) {
        if (typeof value === "string" && value.toLowerCase().includes(q)) return true;
    }
    return false;
}

function fmtTime(ts: number): string {
    return new Date(ts).toLocaleTimeString(undefined, {
        hour12: false,
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
    });
}

function EventRow({ event }: { event: LiveEvent }) {
    const [expanded, setExpanded] = useState(false);
    const ids = idSummary(event.payload);
    const tone = eventTone(event.name);
    return (
        <li className="border-b border-border/70 last:border-0">
            <button
                type="button"
                aria-expanded={expanded}
                onClick={() => setExpanded((v) => !v)}
                className={cn(
                    "group flex min-h-10 w-full flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2 text-left transition-colors outline-none hover:bg-accent/50 focus-visible:bg-accent/60",
                    expanded && "bg-accent/40",
                )}
            >
                <ChevronRight
                    className={cn(
                        "size-3.5 shrink-0 text-subtle-foreground transition-transform",
                        expanded && "rotate-90",
                    )}
                />
                <span className="w-16 shrink-0 font-mono text-[11.5px] tabular-nums text-subtle-foreground">
                    {fmtTime(event.receivedAt)}
                </span>
                <span className="inline-flex min-w-0 items-center gap-2">
                    <span className={cn("size-1.5 shrink-0 rounded-full", TONE_DOT[tone])} />
                    <span
                        className={cn(
                            "truncate font-mono text-[12px] font-medium",
                            tone === "neutral" ? "text-foreground" : TONE_TEXT[tone],
                        )}
                    >
                        {event.name}
                    </span>
                </span>
                <span className="flex min-w-0 flex-wrap items-center gap-1.5 font-mono text-[11px] text-muted-foreground sm:ml-auto">
                    {ids.map((id) => (
                        <span
                            key={id.key}
                            className="inline-flex h-5 items-center gap-1 rounded-[5px] border border-border bg-muted/50 px-1.5"
                        >
                            <span className="text-subtle-foreground">{id.key}</span>
                            <span className="text-foreground/80">{id.value}</span>
                        </span>
                    ))}
                </span>
            </button>
            {expanded && (
                <div className="border-t border-border/70 bg-muted/30 px-4 py-3">
                    <pre className="max-h-72 overflow-auto rounded-md border border-border bg-card p-3 font-mono text-[11.5px] leading-relaxed text-foreground">
                        {JSON.stringify(event.payload, null, 2)}
                    </pre>
                </div>
            )}
        </li>
    );
}

// -------------------------------------------------------------------- page

export default function EventsPage() {
    const { events, paused, missedCount } = useLiveEvents();
    const status = useRealtimeStatus();
    const [query, setQuery] = useState("");
    const [family, setFamily] = useState<FamilyId>("all");

    const filtered = useMemo(() => {
        const q = query.trim().toLowerCase();
        return events.filter(
            (ev) => inFamily(ev.name, family) && (q === "" || matchesQuery(ev, q)),
        );
    }, [events, family, query]);

    return (
        <div>
            <PageHeader
                title="Live events"
                meta={
                    <>
                        <ConnectionStatus status={status} />
                        {paused && (
                            <StatusBadge tone="warning" className="ml-1">
                                Paused
                            </StatusBadge>
                        )}
                    </>
                }
                description="Every realtime event on the platform as it happens, mirrored from the org, user, and entity channels."
            >
                <Button variant="outline" size="sm" onClick={() => setPaused(!paused)}>
                    {paused ? <Play /> : <Pause />}
                    {paused
                        ? `Resume${missedCount > 0 ? ` (${missedCount.toLocaleString()} missed)` : ""}`
                        : "Pause"}
                </Button>
                <Button variant="ghost" size="sm" onClick={clearEvents} disabled={events.length === 0}>
                    <Trash2 />
                    Clear
                </Button>
            </PageHeader>

            <div className="mb-3 flex flex-wrap items-center gap-x-3 gap-y-2">
                <div className="relative w-full sm:w-64">
                    <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-subtle-foreground" />
                    <Input
                        value={query}
                        onChange={(e) => setQuery(e.target.value)}
                        placeholder="Filter by event name or id"
                        className="h-7 pl-8 text-[12.5px]"
                    />
                </div>
                <div className="no-scrollbar -mx-1 flex max-w-full items-center gap-1 overflow-x-auto px-1">
                    {FAMILY_CHIPS.map((chip) => {
                        const active = family === chip.id;
                        return (
                            <button
                                key={chip.id}
                                type="button"
                                aria-pressed={active}
                                onClick={() => setFamily(chip.id)}
                                className={cn(
                                    "h-7 shrink-0 whitespace-nowrap rounded-md border px-2.5 text-[12.5px] font-medium transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
                                    active
                                        ? "border-border-strong bg-accent text-foreground"
                                        : "border-transparent text-muted-foreground hover:bg-accent/60 hover:text-foreground",
                                )}
                            >
                                {chip.label}
                            </button>
                        );
                    })}
                </div>
                <span className="ml-auto hidden text-xs text-muted-foreground tabular-nums sm:inline">
                    {filtered.length.toLocaleString()} of {events.length.toLocaleString()}
                </span>
            </div>

            <div className="overflow-hidden surface-lit rounded-xl border border-border bg-card">
                {filtered.length === 0 ? (
                    <EmptyState
                        icon={Radio}
                        title={status === "refused" ? "Live events are not available" : events.length === 0 ? "No events yet" : "No matching events"}
                        hint={
                            status === "refused"
                                ? (refusalMessage(getRefusal()) ?? "The live event feed refused this session. Sign in again.")
                                : events.length === 0
                                  ? "Events appear here as platform activity happens. The connection status is shown above."
                                  : "Try a different filter or family chip."
                        }
                    />
                ) : (
                    <ul>
                        {filtered.map((ev) => (
                            <EventRow key={ev.id} event={ev} />
                        ))}
                    </ul>
                )}
            </div>

            <p className="mt-2.5 text-xs text-muted-foreground">
                Showing the last {Math.min(events.length, MAX_EVENTS).toLocaleString()} events
                (buffer capped at {MAX_EVENTS}). Older events are dropped.
            </p>
        </div>
    );
}
