import { describe, expect, it } from "vitest";
import { resolveSignature, senderFields, unknownPlaceholders } from "./signaturePlaceholders";
import { signatureToPlain } from "./signaturePlain";

const addr = (local: string, domain: string) => `${local}@${domain}`;
const JANE = addr("jane", "acme.test");

describe("senderFields", () => {
    it("splits the sender name on the first whitespace", () => {
        expect(senderFields({ name: "Jane Doe", email: JANE })).toEqual({
            FirstName: "Jane",
            LastName: "Doe",
            Name: "Jane Doe",
            Email: JANE,
            named: true,
        });
    });

    it("keeps everything after the first word as the last name, spaces collapsed", () => {
        const f = senderFields({ name: "  Mary   Ann  van Dyke ", email: addr("mary", "acme.test") });
        expect(f).toMatchObject({ FirstName: "Mary", LastName: "Ann van Dyke", Name: "Mary Ann van Dyke" });
    });

    it("has no last name for a single-word name", () => {
        expect(senderFields({ name: "Cher", email: addr("cher", "acme.test") })).toMatchObject({ FirstName: "Cher", LastName: "", Name: "Cher" });
    });

    it("leaves every name field empty when the mailbox has no name", () => {
        expect(senderFields({ name: "   ", email: addr("sales.team", "acme.test") })).toEqual({
            FirstName: "",
            LastName: "",
            Name: "",
            Email: addr("sales.team", "acme.test"),
            named: false,
        });
    });

    it("uses the send-as alias as the email, like the From address", () => {
        expect(senderFields({ name: "Jane", email: JANE, send_as_email: ` ${addr("hello", "acme.test")} ` }).Email).toBe(addr("hello", "acme.test"));
        expect(senderFields({ name: "Jane", email: JANE, send_as_email: "  " }).Email).toBe(JANE);
    });
});

describe("resolveSignature", () => {
    const jane = { name: "Jane Doe", email: JANE };

    it("fills every sender field, repeated and spaced ones included", () => {
        const out = resolveSignature(
            "{{.Sender.FirstName}} {{.Sender.LastName}} ({{ .Sender.Name }}) {{.Sender.Email}} {{.Sender.FirstName}}",
            jane,
            { html: false },
        );
        expect(out).toBe(`Jane Doe (Jane Doe) ${JANE} Jane`);
    });

    it("escapes values going into markup, attributes included", () => {
        const sender = { name: `Tom & "Jerry" <x>`, email: addr("tom", "acme.test") };
        const out = resolveSignature(`<a href="mailto:{{.Sender.Email}}" title="{{.Sender.Name}}">{{.Sender.FirstName}}</a>`, sender, {
            html: true,
        });
        expect(out).toBe(`<a href="mailto:${addr("tom", "acme.test")}" title="Tom &amp; &quot;Jerry&quot; &lt;x&gt;">Tom</a>`);
    });

    it("leaves plain text unescaped", () => {
        expect(resolveSignature("{{.Sender.Name}}", { name: "A & B", email: addr("ab", "acme.test") }, { html: false })).toBe("A & B");
    });

    it("renders empty name fields for a mailbox with no name, as a campaign email would", () => {
        expect(resolveSignature("{{.Sender.FirstName}}|{{.Sender.Name}}", { name: "", email: addr("ops", "acme.test") }, { html: true })).toBe("|");
    });

    it("does not touch contact fields, spintax or other sender fields", () => {
        const t = "{{.FirstName}} {Hi|Hello} {{.Sender.Title}} 100% width:50%";
        expect(resolveSignature(t, jane, { html: true })).toBe(t);
    });

    it("plain derived from the resolved HTML carries the decoded value", () => {
        const html = resolveSignature("<p>{{.Sender.Name}}</p><p>{{.Sender.Email}}</p>", { name: "A & B", email: addr("ab", "acme.test") }, { html: true });
        expect(signatureToPlain(html)).toBe(`A & B\n${addr("ab", "acme.test")}`);
    });
});

describe("unknownPlaceholders", () => {
    it("reports every token a signature cannot fill, once each", () => {
        expect(unknownPlaceholders("{{.FirstName}} {{ .Sender.Name }} {{.Sender.Title}} {{.FirstName}}")).toEqual([
            "{{.FirstName}}",
            "{{.Sender.Title}}",
        ]);
    });

    it("ignores spintax, percentages and URL escapes", () => {
        expect(unknownPlaceholders(`<td width="100%">a%20b</td> {Hi|Hello} {{.Sender.Email}}`)).toEqual([]);
    });
});
