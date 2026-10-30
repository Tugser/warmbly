import Request from "../../Request";
import type PlanOffer from "@/lib/api/models/app/subscription/PlanOffer";
import type { PlanOfferCheckoutResult } from "@/lib/api/models/app/subscription/PlanOffer";

export async function listPlanOffers(): Promise<PlanOffer[]> {
    const res = await Request<{ offers: PlanOffer[] }>({ method: "GET", url: "/subscription/offers", authorization: true });
    return res.offers ?? [];
}

export async function startPlanOfferCheckout(planId: string, interval: "month" | "year"): Promise<PlanOfferCheckoutResult> {
    return await Request<PlanOfferCheckoutResult>({
        method: "POST",
        url: `/subscription/offers/${encodeURIComponent(planId)}/checkout`,
        data: { interval },
        authorization: true,
    });
}
