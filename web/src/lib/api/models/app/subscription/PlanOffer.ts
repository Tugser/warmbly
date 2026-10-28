/** GET /subscription/offers: a private plan Warmbly has offered this workspace. */
export default interface PlanOffer {
    plan_id: string;
    name: string;
    currency: string;
    monthly_usd: number;
    yearly_usd?: number;
    monthly_available: boolean;
    yearly_available: boolean;
    /** False when billing is off or no Stripe price is attached yet. */
    available: boolean;
    /** Warming only, like the Warmup plan (the Partnership plan). */
    warmup_only: boolean;
    expires_at?: string | null;
}

/** POST /subscription/offers/:planId/checkout: a Stripe checkout, or a plan change on an existing subscription. */
export interface PlanOfferCheckoutResult {
    checkout_url?: string;
    changed?: boolean;
}
