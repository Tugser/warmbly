import { describe, expect, it } from "vitest";
import { resetLinkToken } from "./resetLinkToken";

describe("resetLinkToken", () => {
    it("reads the token from the fragment", () => {
        expect(resetLinkToken("#session=a.b.c", new URLSearchParams())).toBe("a.b.c");
    });

    it("still reads a link that carries it in the query", () => {
        expect(resetLinkToken("", new URLSearchParams("session=a.b.c"))).toBe("a.b.c");
    });

    it("prefers the fragment when both are present", () => {
        expect(resetLinkToken("#session=frag", new URLSearchParams("session=query"))).toBe("frag");
    });

    it("is empty without a token", () => {
        expect(resetLinkToken("#other=1", new URLSearchParams("to=x"))).toBe("");
    });
});
