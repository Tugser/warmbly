package pipedrive

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// Inside Pipedrive: a JSON panel on person and deal pages showing the
// person's Warmbly outreach, and JSON modals that add them to a campaign or
// pause and resume them. Pipedrive signs every call with a JWT over the app's
// client secret; its company picks the workspace and its user the member.

// Leads enrolls contacts in campaigns and reads their campaign state (the
// contact service). Nil disables the in-Pipedrive actions.
type Leads interface {
	Add(ctx context.Context, userID string, orgID uuid.UUID, contacts []models.AddContact) ([]models.Contact, *errx.Error)
	BulkUpdate(ctx context.Context, userID string, orgID uuid.UUID, data *models.BulkEditContactsData) ([]models.Contact, *errx.Error)
	CampaignStates(ctx context.Context, orgID, contactID uuid.UUID) ([]models.ContactCampaignState, *errx.Error)
}

// AppCall is one panel or modal request: who Pipedrive says is asking, and
// about which record.
type AppCall struct {
	CompanyID string
	UserID    string
	Resource  string
	RecordID  string
}

// appTokenMaxAge bounds how old an issued-at panel or modal token may be.
const appTokenMaxAge = 15 * time.Minute

// VerifyAppToken checks the JWT Pipedrive passes as ?token=: HS256 over the
// client secret, naming the same user and company as the query.
func (s *Service) VerifyAppToken(token string, call AppCall) bool {
	if s.d.ClientSecret == "" || token == "" || call.CompanyID == "" || call.UserID == "" {
		return false
	}
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
		return []byte(s.d.ClientSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !parsed.Valid {
		return false
	}
	if !appTokenFresh(claims, time.Now()) {
		return false
	}
	return claimID(claims["userId"]) == call.UserID && claimID(claims["companyId"]) == call.CompanyID
}

// appTokenMaxLifetime bounds how far ahead an expiry-only token may be valid.
const appTokenMaxLifetime = 24 * time.Hour

// appTokenSkew tolerates clock drift between Pipedrive and this instance.
const appTokenSkew = time.Minute

// appTokenFresh requires a bounded lifetime: an iat within appTokenMaxAge, or
// an exp (already checked unexpired by the parser) at most appTokenMaxLifetime away.
func appTokenFresh(claims jwt.MapClaims, now time.Time) bool {
	if iat, err := claims.GetIssuedAt(); err == nil && iat != nil {
		return !iat.After(now.Add(appTokenSkew)) && now.Sub(iat.Time) <= appTokenMaxAge
	}
	if exp, err := claims.GetExpirationTime(); err == nil && exp != nil {
		return exp.Sub(now) <= appTokenMaxLifetime
	}
	return false
}

func claimID(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatInt(int64(x), 10)
	}
	return ""
}

// companyOrg resolves the one workspace running Pipedrive mode for a company.
func (s *Service) companyOrg(ctx context.Context, companyID string) (*org, *errx.Error) {
	ids, err := s.d.Repo.OrgsForCompany(ctx, provider, strings.TrimSpace(companyID))
	if err != nil {
		return nil, errx.InternalError()
	}
	if len(ids) == 0 {
		return nil, errx.NewWithIdentifier(errx.NotFound, "crm_not_connected",
			"This Pipedrive company is not connected to a Warmbly workspace in Pipedrive mode.")
	}
	// A company must name exactly one workspace, or nothing is chosen for it.
	if len(ids) > 1 {
		return nil, errx.New(errx.Conflict,
			"This Pipedrive company is connected to more than one Warmbly workspace in Pipedrive mode. Keep Pipedrive mode on in one of them.")
	}
	o, err := s.resolve(ctx, ids[0])
	if err != nil || o == nil {
		return nil, errx.NewWithIdentifier(errx.NotFound, "crm_not_connected", "Pipedrive mode is off for this workspace.")
	}
	return o, nil
}

// appMember is the member an app call acts as: the one matched to the
// Pipedrive user, holding every permission in perm.
func (s *Service) appMember(ctx context.Context, o *org, userID string, perm models.OrganizationPermission) (string, *errx.Error) {
	member, perms, err := s.d.Repo.MemberForOwner(ctx, o.ID, provider, userID)
	if err != nil {
		return "", errx.InternalError()
	}
	if member == nil {
		return "", errx.New(errx.Forbidden,
			"Your Pipedrive user is not matched to a Warmbly member. Ask a workspace admin to match you under Users on the Pipedrive page in Warmbly.")
	}
	if !perms.HasPermission(perm) {
		return "", errx.New(errx.Forbidden, "Your Warmbly role does not allow this. Ask a workspace admin for access.")
	}
	return member.String(), nil
}

// appPerson is the Pipedrive person a call is about: the record itself, or a
// deal's person.
func (s *Service) appPerson(ctx context.Context, o *org, call AppCall) (int64, error) {
	n := extID(call.RecordID)
	if n == 0 {
		return 0, nil
	}
	if call.Resource != "deal" {
		return n, nil
	}
	d, err := o.Client.GetDeal(ctx, n)
	if err != nil || d.PersonID == nil {
		return 0, err
	}
	return *d.PersonID, nil
}

// appContact finds the Warmbly contact behind a Pipedrive person, linking it
// by email when Warmbly has the address. person is read when it had to be.
func (s *Service) appContact(ctx context.Context, o *org, personID int64) (*models.Contact, *Person, error) {
	if rec, err := s.d.Repo.GetContactRecordByExternal(ctx, o.ID, provider, id(personID)); err != nil {
		return nil, nil, err
	} else if rec != nil {
		cs, xerr := s.d.Contacts.GetByIDsAndOrganization(ctx, o.ID, []uuid.UUID{rec.ContactID})
		if xerr != nil {
			return nil, nil, xerr
		}
		if len(cs) == 1 {
			return &cs[0], nil, nil
		}
	}
	p, err := o.Client.GetPerson(ctx, personID)
	if err != nil {
		return nil, nil, err
	}
	if email := p.Email(); email != "" {
		if c, xerr := s.d.Contacts.GetByEmailAndOrganization(ctx, o.ID, email); xerr == nil && c != nil {
			if _, _, err := s.storeContactRecord(ctx, o, c.ID, p); err != nil {
				return nil, nil, err
			}
			return c, p, nil
		}
	}
	return nil, p, nil
}

// PanelLabel is a coloured label field in a JSON panel.
type PanelLabel struct {
	Color string `json:"color"`
	Label string `json:"label"`
}

// PanelLink is a link field in a JSON panel.
type PanelLink struct {
	Label    string `json:"label"`
	Value    string `json:"value"`
	External bool   `json:"external"`
}

// PanelRow is one object of the panel, in the shape its schema declares
// (pipedrive-app/panel.schema.json).
type PanelRow struct {
	ID       int         `json:"id"`
	Header   string      `json:"header"`
	Status   *PanelLabel `json:"status,omitempty"`
	Progress string      `json:"progress,omitempty"`
	Note     string      `json:"note,omitempty"`
	LastSeen string      `json:"last_activity,omitempty"`
	Open     *PanelLink  `json:"open,omitempty"`
}

// Panel is the JSON panel answer.
type Panel struct {
	Data         []PanelRow         `json:"data"`
	ExternalLink *PanelExternalLink `json:"external_link,omitempty"`
}

// PanelExternalLink is the panel's "Open in Warmbly".
type PanelExternalLink struct {
	URL   string `json:"url"`
	Label string `json:"label"`
}

var leadColors = map[string]PanelLabel{
	models.LeadStatusPending:       {Color: "grey", Label: "Waiting"},
	models.LeadStatusActive:        {Color: "blue", Label: "Active"},
	models.LeadStatusCompleted:     {Color: "grey", Label: "Finished"},
	models.LeadStatusReplied:       {Color: "green", Label: "Replied"},
	models.LeadStatusBounced:       {Color: "red", Label: "Bounced"},
	models.LeadStatusFailed:        {Color: "red", Label: "Failed"},
	models.LeadStatusUnsubscribed:  {Color: "red", Label: "Unsubscribed"},
	models.LeadStatusPaused:        {Color: "yellow", Label: "Paused"},
	models.LeadStatusUndeliverable: {Color: "red", Label: "Undeliverable"},
}

var statusColors = map[string]string{
	statusContacted: "blue", statusReplied: "purple", statusInterested: "green", statusNotInterest: "grey",
	statusMeeting: "green", statusBounced: "red", statusUnsubscribed: "red",
}

// AppPanel builds the panel for a person or deal page.
func (s *Service) AppPanel(ctx context.Context, call AppCall) (*Panel, *errx.Error) {
	o, xerr := s.companyOrg(ctx, call.CompanyID)
	if xerr != nil {
		return nil, xerr
	}
	if _, xerr := s.appMember(ctx, o, call.UserID, models.PermViewCampaigns|models.PermViewContacts); xerr != nil {
		return nil, xerr
	}
	out := &Panel{Data: []PanelRow{}}
	personID, err := s.appPerson(ctx, o, call)
	if err != nil {
		return nil, s.userError(ctx, o, err)
	}
	if personID == 0 {
		return out, nil
	}
	c, _, err := s.appContact(ctx, o, personID)
	if err != nil {
		return nil, s.userError(ctx, o, err)
	}
	if c == nil {
		return out, nil
	}
	link := s.contactLink(c.ID)
	if link != "" {
		out.ExternalLink = &PanelExternalLink{URL: link, Label: "Open in Warmbly"}
	}
	var states []models.ContactCampaignState
	if s.d.Leads != nil {
		states, _ = s.d.Leads.CampaignStates(ctx, o.ID, c.ID)
	}
	status := "Not contacted"
	if rec, _ := s.d.Repo.GetContactRecord(ctx, o.ID, c.ID, provider); rec != nil && rec.Properties[statusKey] != "" {
		status = rec.Properties[statusKey]
	}
	summary := PanelRow{
		ID: 1, Header: "Warmbly",
		Status:   &PanelLabel{Color: firstNonEmpty(statusColors[status], "grey"), Label: status},
		Progress: campaignCount(len(states)),
	}
	if link != "" {
		summary.Open = &PanelLink{Label: "Open in Warmbly", Value: link, External: true}
	}
	out.Data = append(out.Data, summary)
	for i, st := range states {
		if len(out.Data) == 10 {
			break
		}
		row := PanelRow{ID: i + 2, Header: truncateRunes(st.CampaignName, 120)}
		label, ok := leadColors[st.LeadStatus]
		if !ok {
			label = PanelLabel{Color: "grey", Label: firstNonEmpty(st.LeadStatus, "Waiting")}
		}
		if st.Hold != nil {
			label = PanelLabel{Color: "yellow", Label: "Paused"}
			row.Note = truncateRunes(st.Hold.Reason, 250)
		}
		row.Status = &label
		if total := st.TotalSteps; total > 0 {
			row.Progress = stepLabel(st.CompletedSteps, total)
		}
		if st.LastActionAt != nil {
			row.LastSeen = pdTime(*st.LastActionAt)
		}
		if s.d.AppURL != "" {
			row.Open = &PanelLink{Label: "Open campaign", External: true,
				Value: strings.TrimRight(s.d.AppURL, "/") + "/app/campaigns/" + st.CampaignID.String()}
		}
		out.Data = append(out.Data, row)
	}
	return out, nil
}

func campaignCount(n int) string {
	switch n {
	case 0:
		return "In no campaign"
	case 1:
		return "In 1 campaign"
	default:
		return "In " + strconv.Itoa(n) + " campaigns"
	}
}

func stepLabel(done, total int) string {
	if done >= total {
		return "All steps sent"
	}
	return "Step " + strconv.Itoa(done+1) + " of " + strconv.Itoa(total)
}

// ModalItem is one choice in a JSON modal select.
type ModalItem struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// EnrollChoices are the campaigns the enroll modal offers.
func (s *Service) EnrollChoices(ctx context.Context, call AppCall) ([]ModalItem, *errx.Error) {
	o, xerr := s.companyOrg(ctx, call.CompanyID)
	if xerr != nil {
		return nil, xerr
	}
	if _, xerr := s.appMember(ctx, o, call.UserID, models.PermManageCampaigns|models.PermManageContacts); xerr != nil {
		return nil, xerr
	}
	opts, err := s.d.Repo.CampaignOptions(ctx, o.ID, "", 100)
	if err != nil {
		return nil, errx.InternalError()
	}
	out := make([]ModalItem, 0, len(opts))
	for _, c := range opts {
		label := c.Name
		if c.Status != "active" {
			label += " (" + strings.ReplaceAll(c.Status, "_", " ") + ")"
		}
		out = append(out, ModalItem{Label: truncateRunes(label, 120), Value: c.ID.String()})
	}
	return out, nil
}

// AppEnroll adds the person to a campaign as the matched member, creating the
// Warmbly contact from the Pipedrive person when it is new. It returns the
// campaign's name for the confirmation.
func (s *Service) AppEnroll(ctx context.Context, call AppCall, campaignID string) (string, *errx.Error) {
	o, xerr := s.companyOrg(ctx, call.CompanyID)
	if xerr != nil {
		return "", xerr
	}
	actor, xerr := s.appMember(ctx, o, call.UserID, models.PermManageCampaigns|models.PermManageContacts)
	if xerr != nil {
		return "", xerr
	}
	if s.d.Leads == nil {
		return "", errx.New(errx.NotImplemented, "enrolment from Pipedrive is not available on this instance")
	}
	cid, err := uuid.Parse(strings.TrimSpace(campaignID))
	if err != nil {
		return "", errx.New(errx.BadRequest, "Choose a campaign.")
	}
	opts, err := s.d.Repo.CampaignOptions(ctx, o.ID, "", 500)
	if err != nil {
		return "", errx.InternalError()
	}
	name := ""
	for _, c := range opts {
		if c.ID == cid {
			name = c.Name
			break
		}
	}
	if name == "" {
		return "", errx.New(errx.NotFound, "That campaign is not in this workspace or has finished.")
	}
	personID, err := s.appPerson(ctx, o, call)
	if err != nil {
		return "", s.userError(ctx, o, err)
	}
	if personID == 0 {
		return "", errx.New(errx.BadRequest, "This deal has no person to add.")
	}
	c, p, err := s.appContact(ctx, o, personID)
	if err != nil {
		return "", s.userError(ctx, o, err)
	}
	if c != nil {
		if _, xerr := s.d.Leads.BulkUpdate(ctx, actor, o.ID, &models.BulkEditContactsData{
			ContactSelection: models.ContactSelection{Contacts: []string{c.ID.String()}},
			AddCampaigns:     []string{cid.String()},
		}); xerr != nil {
			return "", xerr
		}
		s.notify(ctx, o.ID, c.ID.String(), "contact")
		return name, nil
	}
	if p == nil || p.Email() == "" {
		return "", errx.New(errx.BadRequest, "This person has no email address.")
	}
	if strings.EqualFold(p.MarketingStatus, "unsubscribed") {
		return "", errx.NewWithIdentifier(errx.Conflict, "crm_opted_out", "This person unsubscribed in Pipedrive.")
	}
	company := ""
	if p.OrgID != nil {
		if org, err := o.Client.GetOrganization(ctx, *p.OrgID); err == nil {
			company = org.Name
		}
	}
	first, last := p.FirstName, p.LastName
	if first == "" && last == "" {
		first, last, _ = strings.Cut(strings.TrimSpace(p.Name), " ")
	}
	created, xerr := s.d.Leads.Add(ctx, actor, o.ID, []models.AddContact{{
		Email: p.Email(), FirstName: first, LastName: last, Company: company, Phone: p.Phone(), Campaigns: []string{cid.String()},
	}})
	if xerr != nil {
		return "", xerr
	}
	if len(created) == 1 {
		if _, _, err := s.storeContactRecord(ctx, o, created[0].ID, p); err != nil {
			return "", errx.InternalError()
		}
		s.notify(ctx, o.ID, created[0].ID.String(), "contact")
	}
	return name, nil
}

// AppHeld reports whether any of the person's campaigns is held, for the
// pause modal's preselected choice.
func (s *Service) AppHeld(ctx context.Context, call AppCall) (bool, *errx.Error) {
	o, xerr := s.companyOrg(ctx, call.CompanyID)
	if xerr != nil {
		return false, xerr
	}
	if _, xerr := s.appMember(ctx, o, call.UserID, models.PermManageCampaigns); xerr != nil {
		return false, xerr
	}
	personID, err := s.appPerson(ctx, o, call)
	if err != nil || personID == 0 || s.d.Leads == nil {
		return false, nil
	}
	c, _, err := s.appContact(ctx, o, personID)
	if err != nil || c == nil {
		return false, nil
	}
	states, _ := s.d.Leads.CampaignStates(ctx, o.ID, c.ID)
	for _, st := range states {
		if st.Hold != nil {
			return true, nil
		}
	}
	return false, nil
}

// AppSetPaused holds or resumes every campaign the person is in.
func (s *Service) AppSetPaused(ctx context.Context, call AppCall, paused bool) *errx.Error {
	o, xerr := s.companyOrg(ctx, call.CompanyID)
	if xerr != nil {
		return xerr
	}
	if _, xerr := s.appMember(ctx, o, call.UserID, models.PermManageCampaigns); xerr != nil {
		return xerr
	}
	personID, err := s.appPerson(ctx, o, call)
	if err != nil {
		return s.userError(ctx, o, err)
	}
	c, _, err := s.appContact(ctx, o, personID)
	if err != nil {
		return s.userError(ctx, o, err)
	}
	if c == nil {
		return errx.New(errx.NotFound, "This person is not in Warmbly.")
	}
	if paused {
		if s.d.Holds != nil {
			if _, err := s.d.Holds.HoldLeadEverywhere(ctx, c.ID, nil, "Paused from Pipedrive", models.LeadHoldSourceManual); err != nil {
				return errx.InternalError()
			}
		}
	} else if _, err := s.d.Repo.ResumeHeldEverywhere(ctx, o.ID, c.ID); err != nil {
		return errx.InternalError()
	}
	s.notify(ctx, o.ID, c.ID.String(), "contact")
	return nil
}
