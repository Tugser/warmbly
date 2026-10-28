package templates

import (
	"bytes"
	"html/template"
	"time"

	"github.com/warmbly/warmbly/internal/observability/errs"
)

// Team invitation rendered on the shared base shell. The org name and
// inviter name are interpolated through html/template so they're
// auto-escaped in HTML context.

const invitationContent = `
<p style="margin:0 0 4px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:11px;color:#94a3b8;letter-spacing:0.14em;text-transform:uppercase;font-weight:500;">
Invitation
</p>
<h2 style="margin:0 0 12px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-weight:600;font-size:20px;color:#0f172a;letter-spacing:-0.01em;">
You've been invited to {{.OrgName}}
</h2>
<p style="margin:0 0 20px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:13px;color:#475569;line-height:20px;">
{{.InviterName}} has invited you to join <strong style="color:#0f172a;">{{.OrgName}}</strong> on {{.Brand}}. Accept the invitation to start collaborating on mailboxes, warmup, and campaigns.
</p>

<table cellpadding="0" cellspacing="0" border="0" align="center" role="presentation" style="margin:0 0 24px;">
<tr>
<td align="center" style="border-radius:6px;background:#0f172a;">
<a href="{{.AcceptURL}}" target="_blank" style="display:inline-block;padding:10px 22px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:13px;font-weight:600;color:#ffffff;text-decoration:none;letter-spacing:0.01em;">Accept invitation</a>
</td>
</tr>
</table>

<div style="margin:0 0 16px;height:1px;background:#e2e8f0;"></div>

<p style="margin:0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:12px;color:#64748b;line-height:18px;">
This invitation expires in {{.Expiry}}. If you don't have an account yet, you can create one when you accept.
</p>
`

var invitationTmpl = template.Must(template.New("invitation_content").Parse(invitationContent))

// GenerateInvitationHTML renders a team-invitation email. acceptURL is
// the full invite-accept link (already carrying the token); ttl is its remaining lifetime.
func GenerateInvitationHTML(inviterName, orgName, acceptURL string, ttl time.Duration) (string, error) {
	brand := CompanyName()
	data := struct {
		InviterName string
		OrgName     string
		AcceptURL   string
		Brand       string
		Expiry      string
	}{InviterName: inviterName, OrgName: orgName, AcceptURL: acceptURL, Brand: brand, Expiry: humanExpiry(ttl)}
	var buf bytes.Buffer
	if err := invitationTmpl.Execute(&buf, data); err != nil {
		errs.CaptureException(err)
		return "", err
	}
	return renderEmail("You've been invited to "+brand, buf.String())
}
