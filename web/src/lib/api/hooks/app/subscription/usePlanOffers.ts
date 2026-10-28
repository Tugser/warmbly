import { queryOptions, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { listPlanOffers, startPlanOfferCheckout } from "@/lib/api/client/app/subscription/planOffers";

// Under ["subscription"], so the audit spine's subscription invalidation refreshes it too.
export const planOffersQuery = queryOptions({
    queryKey: ["subscription", "offers"],
    queryFn: () => listPlanOffers(),
    staleTime: 60_000,
});

export default function usePlanOffers(enabled = true) {
    return useQuery({ ...planOffersQuery, enabled });
}

export function useStartPlanOfferCheckout() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: ({ planId, interval }: { planId: string; interval: "month" | "year" }) => startPlanOfferCheckout(planId, interval),
        onSuccess: (res) => {
            if (res.changed) void qc.invalidateQueries({ queryKey: ["subscription"] });
        },
    });
}
