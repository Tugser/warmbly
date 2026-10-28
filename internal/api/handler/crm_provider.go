package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/api/middleware"
	"github.com/warmbly/warmbly/internal/app/crmmode"
	"github.com/warmbly/warmbly/internal/app/hubspot"
	"github.com/warmbly/warmbly/internal/app/pipedrive"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// CRM mode: which CRM the workspace runs on (Warmbly's own, HubSpot or
// Pipedrive), and the connected side of it.

func (h *Handler) crmReady(c *gin.Context) (uuid.UUID, bool) {
	orgID := middleware.GetOrganizationID(c)
	if orgID == nil {
		errx.Handle(c, errx.New(errx.BadRequest, "no organization selected"))
		return uuid.Nil, false
	}
	if h.CRMModes == nil {
		errx.Handle(c, errx.New(errx.NotImplemented, "CRM providers are not available on this instance"))
		return uuid.Nil, false
	}
	return *orgID, true
}

// crmProvider resolves the CRM the workspace runs on.
func (h *Handler) crmProvider(c *gin.Context) (crmmode.Provider, uuid.UUID, bool) {
	orgID, ok := h.crmReady(c)
	if !ok {
		return nil, uuid.Nil, false
	}
	p, xerr := h.CRMModes.For(c.Request.Context(), orgID)
	if xerr != nil {
		errx.Handle(c, xerr)
		return nil, uuid.Nil, false
	}
	return p, orgID, true
}

// GetCRMSettings returns the workspace's CRM mode and the connected account.
func (h *Handler) GetCRMSettings(c *gin.Context) {
	orgID, ok := h.crmReady(c)
	if !ok {
		return
	}
	out, xerr := h.CRMModes.Settings(c.Request.Context(), orgID)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.JSON(http.StatusOK, out)
}

// UpdateCRMSettings switches the CRM and stores the setup choices.
func (h *Handler) UpdateCRMSettings(c *gin.Context) {
	orgID, ok := h.crmReady(c)
	if !ok {
		return
	}
	var body models.UpdateCRMSettings
	if err := c.ShouldBindJSON(&body); err != nil {
		errx.Handle(c, errx.InvalidBody(err))
		return
	}
	out, xerr := h.CRMModes.UpdateSettings(c.Request.Context(), orgID, &body)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	h.auditOrg(c, models.AuditActionUpdate, models.AuditEntityIntegration, out.ConnectionID, nil,
		map[string]string{"crm_provider": string(out.Provider)})
	c.JSON(http.StatusOK, out)
}

// GetCRMMetadata returns the provider's lifecycle stages, lead statuses, task
// types, contact properties and pipelines for the pickers.
func (h *Handler) GetCRMMetadata(c *gin.Context) {
	p, orgID, ok := h.crmProvider(c)
	if !ok {
		return
	}
	out, xerr := p.Metadata(c.Request.Context(), orgID)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.JSON(http.StatusOK, out)
}

// ListCRMOwners lists the provider's owners and the member each one is.
func (h *Handler) ListCRMOwners(c *gin.Context) {
	p, orgID, ok := h.crmProvider(c)
	if !ok {
		return
	}
	out, xerr := p.Owners(c.Request.Context(), orgID)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// MapCRMOwner pins an owner to a member (or clears the match).
func (h *Handler) MapCRMOwner(c *gin.Context) {
	p, orgID, ok := h.crmProvider(c)
	if !ok {
		return
	}
	ext := strings.TrimSpace(c.Param("externalId"))
	if ext == "" || len(ext) > 64 {
		errx.Handle(c, errx.New(errx.BadRequest, "invalid owner id"))
		return
	}
	var body models.MapCRMOwner
	if err := c.ShouldBindJSON(&body); err != nil {
		errx.Handle(c, errx.InvalidBody(err))
		return
	}
	if xerr := p.MapOwner(c.Request.Context(), orgID, ext, body.UserID); xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	h.auditOrg(c, models.AuditActionAssign, models.AuditEntityIntegration, nil, nil, map[string]string{"crm_owner": ext})
	c.Status(http.StatusNoContent)
}

// GetCRMSyncHealth reports what is waiting, what failed and when each pull ran.
func (h *Handler) GetCRMSyncHealth(c *gin.Context) {
	p, orgID, ok := h.crmProvider(c)
	if !ok {
		return
	}
	out, xerr := p.SyncHealth(c.Request.Context(), orgID)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.JSON(http.StatusOK, out)
}

type crmJobSelection struct {
	IDs []uuid.UUID `json:"ids" binding:"max=500"`
}

// RetryCRMSyncFailures requeues failed sync jobs (all, or the ids given).
// Naturally idempotent: requeueing a requeued job changes nothing.
func (h *Handler) RetryCRMSyncFailures(c *gin.Context) {
	p, orgID, ok := h.crmProvider(c)
	if !ok {
		return
	}
	var body crmJobSelection
	if err := c.ShouldBindJSON(&body); err != nil && err != io.EOF {
		errx.Handle(c, errx.InvalidBody(err))
		return
	}
	n, xerr := p.RetryFailed(c.Request.Context(), orgID, body.IDs)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"affected": n})
}

// DiscardCRMSyncFailures drops failed sync jobs (all, or the ids given).
func (h *Handler) DiscardCRMSyncFailures(c *gin.Context) {
	p, orgID, ok := h.crmProvider(c)
	if !ok {
		return
	}
	var body crmJobSelection
	if err := c.ShouldBindJSON(&body); err != nil && err != io.EOF {
		errx.Handle(c, errx.InvalidBody(err))
		return
	}
	n, xerr := p.DiscardFailed(c.Request.Context(), orgID, body.IDs)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"affected": n})
}

// SyncCRMNow starts a full pull from the provider.
func (h *Handler) SyncCRMNow(c *gin.Context) {
	p, orgID, ok := h.crmProvider(c)
	if !ok {
		return
	}
	if xerr := p.SyncNow(c.Request.Context(), orgID); xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.Status(http.StatusAccepted)
}

func (h *Handler) crmContactID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.Handle(c, errx.ErrUuid)
		return uuid.Nil, false
	}
	return id, true
}

// GetCRMContact returns the provider side of a contact: owner, lifecycle stage,
// lead status, company and the chosen properties.
func (h *Handler) GetCRMContact(c *gin.Context) {
	p, orgID, ok := h.crmProvider(c)
	if !ok {
		return
	}
	contactID, ok := h.crmContactID(c)
	if !ok {
		return
	}
	out, xerr := p.ContactView(c.Request.Context(), orgID, contactID)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.JSON(http.StatusOK, out)
}

// RefreshCRMContact pulls a contact's provider record, deals, tasks and notes
// now. Debounced per contact, so it is safe to call whenever a panel opens.
func (h *Handler) RefreshCRMContact(c *gin.Context) {
	p, orgID, ok := h.crmProvider(c)
	if !ok {
		return
	}
	contactID, ok := h.crmContactID(c)
	if !ok {
		return
	}
	out, xerr := p.RefreshContact(c.Request.Context(), orgID, contactID)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.JSON(http.StatusOK, out)
}

// LinkCRMContact finds or creates the contact in the provider.
func (h *Handler) LinkCRMContact(c *gin.Context) {
	p, orgID, ok := h.crmProvider(c)
	if !ok {
		return
	}
	contactID, ok := h.crmContactID(c)
	if !ok {
		return
	}
	out, xerr := p.LinkContact(c.Request.Context(), orgID, contactID)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	h.auditOrg(c, models.AuditActionConnect, models.AuditEntityContact, &contactID, nil, map[string]string{"crm": string(p.Name())})
	c.JSON(http.StatusOK, out)
}

// UpdateCRMContact edits owner, lifecycle stage or lead status in place.
func (h *Handler) UpdateCRMContact(c *gin.Context) {
	p, orgID, ok := h.crmProvider(c)
	if !ok {
		return
	}
	contactID, ok := h.crmContactID(c)
	if !ok {
		return
	}
	var body models.UpdateCRMContact
	if err := c.ShouldBindJSON(&body); err != nil {
		errx.Handle(c, errx.InvalidBody(err))
		return
	}
	for _, v := range []*string{body.OwnerExternalID, body.LifecycleStage, body.LeadStatus} {
		if v != nil && len(*v) > 100 {
			errx.Handle(c, errx.New(errx.BadRequest, "value too long"))
			return
		}
	}
	out, xerr := p.UpdateContactRecord(c.Request.Context(), orgID, contactID, &body)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	h.auditOrg(c, models.AuditActionUpdate, models.AuditEntityContact, &contactID, nil, map[string]string{"crm": string(p.Name())})
	c.JSON(http.StatusOK, out)
}

// ListCRMLists lists the provider's contact lists for import.
func (h *Handler) ListCRMLists(c *gin.Context) {
	p, orgID, ok := h.crmProvider(c)
	if !ok {
		return
	}
	limit := 25
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			errx.Handle(c, errx.New(errx.BadRequest, "limit must be between 1 and 100"))
			return
		}
		limit = n
	}
	q := c.Query("q")
	if len(q) > 200 {
		errx.Handle(c, errx.New(errx.BadRequest, "query too long"))
		return
	}
	out, xerr := p.Lists(c.Request.Context(), orgID, q, c.Query("cursor"), limit)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.JSON(http.StatusOK, out)
}

// PreviewCRMImport counts who a list import brings in and who it skips.
func (h *Handler) PreviewCRMImport(c *gin.Context) {
	p, orgID, ok := h.crmProvider(c)
	if !ok {
		return
	}
	var body models.CRMImportRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		errx.Handle(c, errx.InvalidBody(err))
		return
	}
	out, xerr := p.PreviewImport(c.Request.Context(), orgID, &body)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.JSON(http.StatusOK, out)
}

// ImportCRMList turns a provider list into a contact import draft, finished in
// the regular import review.
func (h *Handler) ImportCRMList(c *gin.Context) {
	p, orgID, ok := h.crmProvider(c)
	if !ok {
		return
	}
	userID, err := middleware.GetUserUUID(c)
	if err != nil {
		errx.Handle(c, errx.New(errx.Unauthorized, "a signed-in member starts an import"))
		return
	}
	var body models.CRMImportRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		errx.Handle(c, errx.InvalidBody(err))
		return
	}
	out, xerr := p.Import(c.Request.Context(), orgID, userID, &body)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	h.auditOrg(c, models.AuditActionImport, models.AuditEntityContact, nil, nil, map[string]string{"crm_list": body.ListID})
	c.JSON(http.StatusCreated, out)
}

// GetCRMBackfill counts the Warmbly-only records a switch would copy.
func (h *Handler) GetCRMBackfill(c *gin.Context) {
	p, orgID, ok := h.crmProvider(c)
	if !ok {
		return
	}
	out, xerr := p.BackfillPreview(c.Request.Context(), orgID)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.JSON(http.StatusOK, out)
}

// StartCRMBackfill copies Warmbly's own deals, tasks and notes into the
// provider once. Idempotent: a second call while one runs is folded into it,
// and copied records are never copied twice.
func (h *Handler) StartCRMBackfill(c *gin.Context) {
	p, orgID, ok := h.crmProvider(c)
	if !ok {
		return
	}
	var body models.CRMBackfillRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		errx.Handle(c, errx.InvalidBody(err))
		return
	}
	if xerr := p.StartBackfill(c.Request.Context(), orgID, &body); xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	h.auditOrg(c, models.AuditActionImport, models.AuditEntityIntegration, nil, nil, map[string]string{"crm_backfill": string(p.Name())})
	c.Status(http.StatusAccepted)
}

// verifyHubSpot checks a request HubSpot signed with the app's client secret
// (v3, timestamped) against the public backend URL it was sent to.
func (h *Handler) verifyHubSpot(c *gin.Context, body []byte) bool {
	u := config.BackendPublicURL() + c.Request.URL.RequestURI()
	return h.HubSpot.VerifySignature(c.Request.Method, u, body,
		c.GetHeader("X-HubSpot-Signature-v3"), c.GetHeader("X-HubSpot-Request-Timestamp"), time.Now()) == nil
}

// hubspotFetchParams are what HubSpot appends to a card's hubspot.fetch call.
var hubspotFetchParams = []string{"portalid", "appid", "userid", "useremail"}

// fromHubSpotFetch reports a request a card sent through hubspot.fetch.
// HubSpot's webhook and workflow action calls carry none of these.
func fromHubSpotFetch(q url.Values) bool {
	for k := range q {
		if slices.Contains(hubspotFetchParams, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

// readHubSpotServer reads a signed webhook or workflow action call, whose
// body names the portal, and refuses one a card sent.
func (h *Handler) readHubSpotServer(c *gin.Context) ([]byte, bool) {
	if h.HubSpot != nil && fromHubSpotFetch(c.Request.URL.Query()) {
		c.Status(http.StatusForbidden)
		return nil, false
	}
	return h.readHubSpot(c)
}

// readHubSpot reads and verifies a signed HubSpot request body.
func (h *Handler) readHubSpot(c *gin.Context) ([]byte, bool) {
	if h.HubSpot == nil {
		c.Status(http.StatusNotFound)
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		c.Status(http.StatusBadRequest)
		return nil, false
	}
	if !h.verifyHubSpot(c, body) {
		c.Status(http.StatusUnauthorized)
		return nil, false
	}
	return body, true
}

// HubSpotWebhook receives HubSpot's app webhooks. It only queues re-reads, so
// a delivery can at most make a pull come sooner.
func (h *Handler) HubSpotWebhook(c *gin.Context) {
	body, ok := h.readHubSpotServer(c)
	if !ok {
		return
	}
	if err := h.HubSpot.HandleWebhook(c.Request.Context(), body); err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	c.Status(http.StatusNoContent)
}

// hubspotCardRequest is what the Warmbly card in HubSpot sends. HubSpot adds
// portalId, userId and userEmail to the query string and signs the request.
type hubspotCardRequest struct {
	ContactID  string `json:"contact_id"`
	Email      string `json:"email"`
	CampaignID string `json:"campaign_id"`
	Paused     bool   `json:"paused"`
}

func (h *Handler) hubspotCardInput(c *gin.Context) (*hubspotCardRequest, *hubspot.User, bool) {
	body, ok := h.readHubSpot(c)
	if !ok {
		return nil, nil, false
	}
	user, ok := hubspotFetchUser(c.Request.URL.Query())
	if !ok {
		errx.Handle(c, errx.New(errx.BadRequest, "invalid request"))
		return nil, nil, false
	}
	var req hubspotCardRequest
	if err := json.Unmarshal(body, &req); err != nil || len(req.ContactID) > 32 || len(req.Email) > 320 {
		errx.Handle(c, errx.New(errx.BadRequest, "invalid request"))
		return nil, nil, false
	}
	return &req, user, true
}

// hubspotFetchUser reads the portal and user HubSpot appended and signed. A
// second value would let the URL name different ones, so exactly one of each
// is accepted.
func hubspotFetchUser(q url.Values) (*hubspot.User, bool) {
	for _, k := range []string{"portalId", "userId", "userEmail"} {
		if len(q[k]) != 1 || strings.TrimSpace(q.Get(k)) == "" {
			return nil, false
		}
	}
	return &hubspot.User{ID: q.Get("userId"), Email: q.Get("userEmail")}, true
}

// HubSpotCard returns the Warmbly card for a HubSpot contact record.
func (h *Handler) HubSpotCard(c *gin.Context) {
	req, user, ok := h.hubspotCardInput(c)
	if !ok {
		return
	}
	out, xerr := h.HubSpot.Card(c.Request.Context(), c.Query("portalId"), *user, req.ContactID, req.Email)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.JSON(http.StatusOK, out)
}

// HubSpotCardEnroll adds the record's contact to a campaign from the card.
// Idempotent: enrolling a lead twice leaves one lead.
func (h *Handler) HubSpotCardEnroll(c *gin.Context) {
	req, user, ok := h.hubspotCardInput(c)
	if !ok {
		return
	}
	if xerr := h.HubSpot.Enroll(c.Request.Context(), c.Query("portalId"), req.ContactID, req.Email, req.CampaignID, user); xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.Status(http.StatusNoContent)
}

// HubSpotCardPause holds or resumes the contact's campaigns from the card.
// Idempotent: it sets a state rather than toggling one.
func (h *Handler) HubSpotCardPause(c *gin.Context) {
	req, user, ok := h.hubspotCardInput(c)
	if !ok {
		return
	}
	if xerr := h.HubSpot.SetPaused(c.Request.Context(), c.Query("portalId"), *user, req.ContactID, req.Email, req.Paused); xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.Status(http.StatusNoContent)
}

// hubspotActionRequest is a custom workflow action execution.
type hubspotActionRequest struct {
	CallbackID string `json:"callbackId"`
	Origin     struct {
		PortalID int64 `json:"portalId"`
	} `json:"origin"`
	Object struct {
		ObjectID   int64             `json:"objectId"`
		Properties map[string]string `json:"properties"`
	} `json:"object"`
	InputFields  map[string]any `json:"inputFields"`
	FetchOptions struct {
		Q string `json:"q"`
	} `json:"fetchOptions"`
}

// HubSpotActionEnroll runs the "Add to Warmbly campaign" workflow action, as
// the workspace owner. A refusal answers FAIL_CONTINUE with the reason.
func (h *Handler) HubSpotActionEnroll(c *gin.Context) {
	body, ok := h.readHubSpotServer(c)
	if !ok {
		return
	}
	var req hubspotActionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	campaign, _ := req.InputFields["campaign"].(string)
	xerr := h.HubSpot.Enroll(c.Request.Context(), strconv.FormatInt(req.Origin.PortalID, 10),
		strconv.FormatInt(req.Object.ObjectID, 10), req.Object.Properties["email"], campaign, nil)
	out := gin.H{"hs_execution_state": "SUCCESS", "warmbly_result": "Added to campaign"}
	if xerr != nil {
		out = gin.H{"hs_execution_state": "FAIL_CONTINUE", "warmbly_result": xerr.Message}
	}
	c.JSON(http.StatusOK, gin.H{"outputFields": out})
}

// HubSpotActionCampaigns lists campaigns for the workflow action's dropdown.
func (h *Handler) HubSpotActionCampaigns(c *gin.Context) {
	body, ok := h.readHubSpotServer(c)
	if !ok {
		return
	}
	var req hubspotActionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	opts, xerr := h.HubSpot.CampaignChoices(c.Request.Context(), strconv.FormatInt(req.Origin.PortalID, 10), req.FetchOptions.Q)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"options": opts, "searchable": true})
}

// PipedriveWebhook receives a Pipedrive webhook for one connection. It only
// queues a re-read, so a delivery can at most make a pull come sooner. A
// connection no workspace runs its CRM on answers 410, so Pipedrive retires
// the webhook.
func (h *Handler) PipedriveWebhook(c *gin.Context) {
	if h.Pipedrive == nil {
		c.Status(http.StatusNotFound)
		return
	}
	connID, err := uuid.Parse(c.Param("connectionId"))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	user, pass, _ := c.Request.BasicAuth()
	if !h.Pipedrive.VerifyWebhook(connID, user, pass) {
		c.Status(http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	live, err := h.Pipedrive.HandleWebhook(c.Request.Context(), connID, body)
	switch {
	case !live:
		c.Status(http.StatusGone)
	case err != nil:
		c.Status(http.StatusBadRequest)
	default:
		c.Status(http.StatusNoContent)
	}
}

// pipedriveAppCall reads and verifies a panel or modal call: the JWT Pipedrive
// signs with the app's client secret, naming the user and company in the query.
func (h *Handler) pipedriveAppCall(c *gin.Context) (pipedrive.AppCall, bool) {
	if h.Pipedrive == nil {
		c.Status(http.StatusNotFound)
		return pipedrive.AppCall{}, false
	}
	// Pipedrive puts its signed token in the query; nothing here may pass the URL on.
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Cache-Control", "no-store")
	q := c.Request.URL.Query()
	for _, k := range []string{"userId", "companyId", "selectedIds", "token"} {
		if len(q[k]) > 1 {
			pipedriveRefuse(c, http.StatusBadRequest, pipedriveReopenMessage)
			return pipedrive.AppCall{}, false
		}
	}
	call := pipedrive.AppCall{
		CompanyID: strings.TrimSpace(q.Get("companyId")),
		UserID:    strings.TrimSpace(q.Get("userId")),
		Resource:  strings.TrimSpace(q.Get("resource")),
		RecordID:  strings.TrimSpace(q.Get("selectedIds")),
	}
	if !h.Pipedrive.VerifyAppToken(q.Get("token"), call) {
		pipedriveRefuse(c, http.StatusUnauthorized, pipedriveReopenMessage)
		return pipedrive.AppCall{}, false
	}
	return call, true
}

// pipedriveReopenMessage answers a request whose Pipedrive signature is
// missing, malformed, too old, or not Pipedrive's.
const pipedriveReopenMessage = "Warmbly could not confirm this request came from Pipedrive, or the page has been open too long. Reload the Pipedrive page and open the Warmbly panel again."

// pipedriveRefuse answers in the shape both Pipedrive panels and modals display.
func pipedriveRefuse(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"error": gin.H{"title": "Warmbly", "subtitle": message, "message": message}})
}

// PipedrivePanel answers the Warmbly JSON panel on Pipedrive person and deal
// pages. A refusal is shown in the panel with its reason.
func (h *Handler) PipedrivePanel(c *gin.Context) {
	call, ok := h.pipedriveAppCall(c)
	if !ok {
		return
	}
	out, xerr := h.Pipedrive.AppPanel(c.Request.Context(), call)
	if xerr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"title": "Warmbly", "subtitle": xerr.Message}})
		return
	}
	c.JSON(http.StatusOK, out)
}

type pipedriveModalBody struct {
	Campaign string `json:"campaign"`
	Action   string `json:"action"`
}

func (h *Handler) pipedriveModalBody(c *gin.Context) (*pipedriveModalBody, bool) {
	var body pipedriveModalBody
	if err := json.NewDecoder(io.LimitReader(c.Request.Body, 64<<10)).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Warmbly could not read the form."}})
		return nil, false
	}
	return &body, true
}

func pipedriveModalError(c *gin.Context, xerr *errx.Error) {
	c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": xerr.Message}})
}

// PipedriveEnroll is the "Add to Warmbly campaign" modal: opening it lists the
// campaigns, submitting it adds the person. Idempotent: enrolling a lead twice
// leaves one lead.
func (h *Handler) PipedriveEnroll(c *gin.Context) {
	call, ok := h.pipedriveAppCall(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if c.Request.Method == http.MethodGet {
		items, xerr := h.Pipedrive.EnrollChoices(ctx, call)
		if xerr != nil {
			pipedriveModalError(c, xerr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"blocks": gin.H{"campaign": gin.H{"items": items}}, "actions": gin.H{}}})
		return
	}
	body, ok := h.pipedriveModalBody(c)
	if !ok {
		return
	}
	name, xerr := h.Pipedrive.AppEnroll(ctx, call, body.Campaign)
	if xerr != nil {
		pipedriveModalError(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": gin.H{"message": "Added to " + name, "type": "snackbar"}})
}

// PipedrivePause is the "Pause or resume in Warmbly" modal. Idempotent: it sets
// a state rather than toggling one.
func (h *Handler) PipedrivePause(c *gin.Context) {
	call, ok := h.pipedriveAppCall(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if c.Request.Method == http.MethodGet {
		held, xerr := h.Pipedrive.AppHeld(ctx, call)
		if xerr != nil {
			pipedriveModalError(c, xerr)
			return
		}
		value := "pause"
		if held {
			value = "resume"
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"blocks": gin.H{"action": gin.H{"value": value}}, "actions": gin.H{}}})
		return
	}
	body, ok := h.pipedriveModalBody(c)
	if !ok {
		return
	}
	if body.Action != "pause" && body.Action != "resume" {
		pipedriveModalError(c, errx.New(errx.BadRequest, "Choose pause or resume."))
		return
	}
	if xerr := h.Pipedrive.AppSetPaused(ctx, call, body.Action == "pause"); xerr != nil {
		pipedriveModalError(c, xerr)
		return
	}
	msg := "Campaigns paused"
	if body.Action == "resume" {
		msg = "Campaigns resumed"
	}
	c.JSON(http.StatusOK, gin.H{"success": gin.H{"message": msg, "type": "snackbar"}})
}
