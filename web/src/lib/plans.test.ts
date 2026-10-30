import { describe, expect, it } from "vitest";
import { getPlan, isAtLeast, isWarmupOnlyPlan } from "@/lib/plans";

describe("Partnership plan", () => {
    it("warms only, like the Warmup plan", () => {
        expect(isWarmupOnlyPlan("partnership")).toBe(true);
        expect(isWarmupOnlyPlan("warmup")).toBe(true);
        expect(isWarmupOnlyPlan("starter")).toBe(false);
    });

    it("never reaches a sending tier", () => {
        expect(isAtLeast("partnership", "starter")).toBe(false);
        expect(isAtLeast("partnership", "warmup")).toBe(true);
    });

    it("resolves by its server name", () => {
        expect(getPlan("Partnership").id).toBe("partnership");
    });
});
