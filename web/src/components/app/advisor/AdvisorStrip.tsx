// The Advisor's only surface: a strip that renders on the page where the
// problem is. Scope it to an entity (this campaign, this mailbox) or to a whole
// dashboard area (everything wrong with deliverability).
//
// It renders nothing at all when there is nothing to say. That is the most
// important property here: a permanent "0 issues" panel trains people to stop
// looking at the space, so when something does appear it is already invisible.

import { useMemo, useState } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { ChevronDownIcon, SparklesIcon } from "lucide-react";
import type {
    AdvisorCategory,
    AdvisorFinding,
    AdvisorSurface,
} from "@/lib/api/models/app/advisor/Advisor";
import { SEVERITY_DOT, SEVERITY_RANK, groupFindings } from "@/lib/api/models/app/advisor/Advisor";
import { SURFACE_FETCH_LIMIT, useAdvisorFindings } from "@/lib/api/hooks/app/advisor/useAdvisor";
import AdvisorCard from "./AdvisorCard";
import AdvisorFixDrawer from "./AdvisorFixDrawer";
import AdvisorGroupCard from "./AdvisorGroupCard";

interface Props {
    surface?: AdvisorSurface;
    category?: AdvisorCategory;
    entityType?: string;
    entityId?: string;
    // limit caps how many cards show at once. On an entity page there are
    // rarely more than two or three; on a surface page the tail is noise.
    limit?: number;
    compact?: boolean;
    className?: string;
    // title overrides the default heading. Empty string hides the heading
    // entirely, for tight spaces like a detail drawer tab.
    title?: string;
}

export default function AdvisorStrip({
    surface,
    category,
    entityType,
    entityId,
    limit = 4,
    compact = false,
    className = "",
    title,
}: Props) {
    const [fixing, setFixing] = useState<AdvisorFinding | null>(null);
    // A compact strip folds to one line: the count and the worst finding.
    // Above a page's own content, a list of suggestions is a hint, not the
    // page, so it stays closed until asked.
    const [expanded, setExpanded] = useState(false);

    // An entity-scoped strip must not fire before its id exists, or it renders
    // the whole org's findings for a beat while the page hydrates.
    const enabled = !entityType || Boolean(entityId);

    // A surface strip has to see the whole run before it can say "19
    // mailboxes"; fetching only what fits on screen would undercount every
    // group. Entity strips are already narrow, so they stay small.
    const fetchLimit = entityId ? limit + 4 : SURFACE_FETCH_LIMIT;

    const { data } = useAdvisorFindings(
        { surface, category, entityType, entityId, limit: fetchLimit },
        enabled,
    );

    const groups = useMemo(() => {
        const list = [...(data ?? [])].sort((a, b) => {
            const rank = SEVERITY_RANK[b.severity] - SEVERITY_RANK[a.severity];
            return rank !== 0 ? rank : b.impact - a.impact;
        });

        // On an entity page every finding is already about that one thing, so
        // there is nothing to collapse and a "3 mailboxes" heading would be a
        // lie. Collapsing only makes sense across a whole surface.
        if (entityId) return list.map((f) => ({ key: f.id, lead: f, members: [f] }));
        return groupFindings(list).slice(0, limit);
    }, [data, limit, entityId]);

    const heading = title ?? "Suggestions";

    return (
        <>
            {groups.length > 0 ? (
            <section className={className} aria-label="Advisor suggestions">
                {heading ? (
                    <div className="mb-1.5 flex items-center gap-1.5">
                        <SparklesIcon className="h-3 w-3 text-slate-400" />
                        <h2 className="text-[10px] font-medium uppercase tracking-[0.14em] text-slate-400">
                            {heading}
                        </h2>
                    </div>
                ) : null}

                {/* Compact strips sit above a page's own content, so they are
                    one bordered list of single lines rather than a stack of
                    cards, folded to a summary line, and nothing opens on its
                    own. */}
                {compact ? (
                    <button
                        type="button"
                        onClick={() => setExpanded((v) => !v)}
                        aria-expanded={expanded}
                        className={`w-full min-h-9 px-2.5 py-1 flex items-center gap-2 text-left rounded-md border border-slate-200 bg-white hover:bg-slate-50/60 transition-colors ${expanded ? "rounded-b-none border-b-0" : ""}`}
                    >
                        <SparklesIcon className="h-3 w-3 text-slate-400 shrink-0" />
                        <span className="text-[12px] font-medium text-slate-900 shrink-0">
                            {groups.length} {groups.length === 1 ? "suggestion" : "suggestions"}
                        </span>
                        {!expanded ? (
                            <span className="flex items-center gap-1.5 min-w-0 text-[11.5px] text-slate-500">
                                <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${SEVERITY_DOT[groups[0].lead.severity]}`} aria-hidden />
                                <span className="truncate">{groups[0].lead.title}</span>
                                {groups.length > 1 ? <span className="shrink-0 text-slate-400">+{groups.length - 1} more</span> : null}
                            </span>
                        ) : null}
                        <ChevronDownIcon className={`ml-auto size-3 text-slate-400 shrink-0 transition-transform ${expanded ? "rotate-180" : ""}`} />
                    </button>
                ) : null}
                <AnimatePresence initial={false}>
                {!compact || expanded ? (
                <motion.div
                    key="list"
                    initial={compact ? { height: 0, opacity: 0 } : false}
                    animate={{ height: "auto", opacity: 1 }}
                    exit={{ height: 0, opacity: 0 }}
                    transition={{ duration: 0.18, ease: "easeOut" }}
                    className={compact ? "overflow-hidden rounded-b-md border border-slate-200 bg-white" : ""}
                >
                <div className={compact ? "divide-y divide-slate-200/60 border-t border-slate-200/60" : "space-y-1.5"}>
                    <AnimatePresence initial={false} mode="popLayout">
                        {groups.map((group) => (
                            <motion.div
                                key={group.key}
                                layout
                                initial={{ opacity: 0, y: -4 }}
                                animate={{ opacity: 1, y: 0 }}
                                exit={{ opacity: 0, height: 0, marginBottom: 0 }}
                                transition={{ duration: 0.16, ease: "easeOut" }}
                            >
                                {group.members.length > 1 ? (
                                    <AdvisorGroupCard group={group} onFix={setFixing} />
                                ) : (
                                    <AdvisorCard
                                        finding={group.lead}
                                        onFix={setFixing}
                                        compact={compact}
                                        defaultOpen={!compact && groups.length === 1 && group.lead.severity === "critical"}
                                    />
                                )}
                            </motion.div>
                        ))}
                    </AnimatePresence>
                </div>
                </motion.div>
                ) : null}
                </AnimatePresence>
            </section>
            ) : null}

            <AnimatePresence>
                {fixing ? (
                    <AdvisorFixDrawer finding={fixing} onClose={() => setFixing(null)} />
                ) : null}
            </AnimatePresence>
        </>
    );
}
