// Checkout for a private plan Warmbly offered this workspace (the Partnership
// plan). Same anatomy as WarmupPlanDialog: what is being bought, for which
// workspace, how often it is billed. The price comes from /subscription/offers
// and the server resolves the Stripe price, so nothing here decides a charge.

import React from "react";
import { createPortal } from "react-dom";
import { AnimatePresence, motion } from "framer-motion";
import toast from "react-hot-toast/headless";
import { ArrowRightIcon, CheckIcon, Loader2Icon, XIcon } from "lucide-react";
import { useAppStore } from "@/stores";
import { useStartPlanOfferCheckout } from "@/lib/api/hooks/app/subscription/usePlanOffers";
import type PlanOffer from "@/lib/api/models/app/subscription/PlanOffer";
import type { AppError } from "@/lib/api/client/normalizeError";
import buildError from "@/lib/helper/buildError";
import BillingIntervalToggle from "@/components/app/billing/BillingIntervalToggle";
import { PLAN_ACCENT_CLASSES, WARMUP_PLAN_BENEFITS, getPlan } from "@/lib/plans";

type Interval = "month" | "year";

function fmtDate(ts?: string | null): string | null {
    if (!ts) return null;
    return new Date(ts).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}

export default function OfferedPlanDialog({
    offer,
    onClose,
    switching,
}: {
    offer: PlanOffer | null;
    onClose: () => void;
    /** The workspace already pays Stripe, so this changes its plan instead of opening a checkout. */
    switching: boolean;
}) {
    const open = offer !== null;
    const orgName = useAppStore((s) => s.currentOrganization?.name);
    const checkout = useStartPlanOfferCheckout();
    const [interval, setInterval] = React.useState<Interval>("month");

    // Defaulted once per opening so a refetch never moves a choice already made.
    const defaultedFor = React.useRef<string | null>(null);
    React.useEffect(() => {
        if (!offer) {
            defaultedFor.current = null;
            return;
        }
        if (defaultedFor.current === offer.plan_id) return;
        defaultedFor.current = offer.plan_id;
        setInterval(offer.monthly_available || !offer.yearly_available ? "month" : "year");
    }, [offer]);

    const cardRef = React.useRef<HTMLDivElement>(null);
    React.useEffect(() => {
        if (!open) return;
        const previous = document.activeElement as HTMLElement | null;
        cardRef.current?.focus();
        const onKey = (e: KeyboardEvent) => {
            if (e.key === "Escape" && !checkout.isPending) {
                e.stopPropagation();
                onClose();
            }
        };
        document.addEventListener("keydown", onKey, true);
        return () => {
            document.removeEventListener("keydown", onKey, true);
            previous?.focus?.();
        };
    }, [open, checkout.isPending, onClose]);

    const available = offer?.available === true;
    const monthly = offer?.monthly_usd ?? 0;
    const yearly = offer?.yearly_usd ?? 0;
    const annual = interval === "year" && offer?.yearly_available === true;
    const periodAvailable = annual ? offer?.yearly_available === true : offer?.monthly_available === true;
    const accent = PLAN_ACCENT_CLASSES[getPlan("partnership").accent];
    const expires = fmtDate(offer?.expires_at);

    async function start() {
        if (!offer || checkout.isPending || !available || !periodAvailable) return;
        try {
            const res = await checkout.mutateAsync({ planId: offer.plan_id, interval: annual ? "year" : "month" });
            if (res.changed) {
                toast.success(`You're on ${offer.name} now`);
                onClose();
                return;
            }
            if (!res.checkout_url) throw new Error("no checkout url");
            window.location.href = res.checkout_url;
        } catch (e) {
            toast.error(buildError(e as AppError));
        }
    }

    return createPortal(
        <AnimatePresence>
            {offer && (
                <motion.div
                    initial={{ opacity: 0 }}
                    animate={{ opacity: 1 }}
                    exit={{ opacity: 0 }}
                    transition={{ duration: 0.16 }}
                    onMouseDown={(e) => {
                        if (e.target === e.currentTarget && !checkout.isPending) onClose();
                    }}
                    className="fixed inset-0 z-[160] flex items-center justify-center bg-slate-900/40 backdrop-blur-[2px] px-4"
                >
                    <motion.div
                        role="dialog"
                        aria-modal="true"
                        aria-labelledby="offered-plan-title"
                        data-floating
                        ref={cardRef}
                        tabIndex={-1}
                        initial={{ opacity: 0, y: 8 }}
                        animate={{ opacity: 1, y: 0 }}
                        exit={{ opacity: 0, y: 8 }}
                        transition={{ duration: 0.18, ease: [0.22, 1, 0.36, 1] }}
                        onMouseDown={(e) => e.stopPropagation()}
                        className="w-full max-w-[480px] max-h-[calc(100dvh-3rem)] overflow-y-auto rounded-lg bg-white border border-slate-200 shadow-[0_24px_48px_-12px_rgba(15,23,42,0.18)]"
                    >
                        <div className="h-12 px-4 border-b border-slate-200 flex items-center gap-2.5 sticky top-0 bg-white">
                            <span className="text-[10px] uppercase tracking-[0.14em] text-slate-400 font-medium">Offered plan</span>
                            <div className="h-4 w-px bg-slate-200" />
                            <span id="offered-plan-title" className="text-[12.5px] text-slate-900 font-medium truncate">
                                {offer.name}
                            </span>
                            <button
                                type="button"
                                onClick={onClose}
                                disabled={checkout.isPending}
                                aria-label="Close"
                                className="ml-auto size-7 rounded-md text-slate-500 hover:text-slate-900 hover:bg-slate-100 inline-flex items-center justify-center transition-colors disabled:opacity-50"
                            >
                                <XIcon className="w-3.5 h-3.5" />
                            </button>
                        </div>

                        <div className="px-5 py-5">
                            <div className="flex items-center gap-2">
                                <span className="text-[12px] uppercase tracking-[0.1em] font-semibold text-slate-800">{offer.name}</span>
                                {available && offer.yearly_available && offer.monthly_available && (
                                    <div className="ml-auto">
                                        <BillingIntervalToggle
                                            interval={interval === "year" ? "annual" : "monthly"}
                                            onChange={(i) => setInterval(i === "annual" ? "year" : "month")}
                                        />
                                    </div>
                                )}
                            </div>
                            <p className="mt-1.5 text-[12.5px] text-slate-500 leading-snug">
                                {offer.warmup_only
                                    ? "Offered to this workspace by Warmbly: the premium warmup pool for every mailbox, with no mailbox cap."
                                    : "Offered to this workspace by Warmbly."}
                            </p>

                            <div className="mt-4 flex items-baseline gap-1.5">
                                <span className="text-[34px] font-semibold tracking-[-0.03em] text-slate-900 tabular-nums">
                                    ${annual ? Math.round(yearly / 12) : monthly}
                                </span>
                                <span className="text-[12px] text-slate-500">/ mo</span>
                            </div>
                            <div className="mt-1 text-[11px] text-slate-400 tabular-nums">
                                {annual ? `billed $${yearly} annually` : "billed monthly"}
                            </div>

                            {offer.warmup_only && (
                                <ul className="mt-5 space-y-3">
                                    {WARMUP_PLAN_BENEFITS.map((b) => (
                                        <li key={b.title} className="flex items-start gap-2.5">
                                            <CheckIcon className="w-3.5 h-3.5 mt-0.5 text-emerald-600 shrink-0" aria-hidden="true" />
                                            <span className="text-[12.5px] leading-snug">
                                                <span className="font-medium text-slate-800">{b.title}.</span>{" "}
                                                <span className="text-slate-500">{b.body}</span>
                                            </span>
                                        </li>
                                    ))}
                                </ul>
                            )}

                            {!available && (
                                <div className="mt-4 rounded-md border border-amber-200 bg-amber-50 px-3 py-2.5">
                                    <p className="text-[12px] text-amber-900">
                                        This plan has no price set yet, so it cannot be bought here. Warmbly will let you know when it can.
                                    </p>
                                </div>
                            )}

                            <p className="mt-5 text-[11.5px] text-slate-400 leading-relaxed">
                                Applies to <span className="text-slate-600 font-medium">{orgName || "this workspace"}</span>
                                {offer.warmup_only ? " and any instance linked to it" : ""}.{" "}
                                {switching ? "Replaces your current plan on the same subscription, prorated." : "Billed through Stripe. Cancel anytime."}
                                {expires ? ` The offer is open until ${expires}.` : ""}
                            </p>
                        </div>

                        <div className="px-4 py-3 border-t border-slate-200 flex items-center justify-end gap-2">
                            <button
                                type="button"
                                onClick={onClose}
                                disabled={checkout.isPending}
                                className="h-8 px-3 rounded-md border border-slate-200 text-slate-700 text-[12.5px] font-medium hover:bg-slate-50 transition-colors disabled:opacity-50"
                            >
                                Not now
                            </button>
                            <button
                                type="button"
                                onClick={start}
                                disabled={!available || !periodAvailable || checkout.isPending}
                                className={`h-8 px-3 rounded-md text-[12.5px] font-medium inline-flex items-center gap-1.5 transition-colors disabled:opacity-50 ${accent.button}`}
                            >
                                {checkout.isPending ? <Loader2Icon className="w-3.5 h-3.5 animate-spin" /> : <ArrowRightIcon className="w-3.5 h-3.5" />}
                                {switching ? `Switch to ${offer.name}` : `Subscribe to ${offer.name}`}
                            </button>
                        </div>
                    </motion.div>
                </motion.div>
            )}
        </AnimatePresence>,
        document.body,
    );
}
