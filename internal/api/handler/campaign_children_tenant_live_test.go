package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/api/middleware"
	"github.com/warmbly/warmbly/internal/app/advanced"
	"github.com/warmbly/warmbly/internal/app/audit"
	"github.com/warmbly/warmbly/internal/app/campaign"
	"github.com/warmbly/warmbly/internal/app/organization"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/infrastructure/storage"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// Attachments, A/B variants and advanced settings of one workspace's campaign are unreachable from another workspace.
func TestLiveCampaignChildrenRefuseAnotherWorkspace(t *testing.T) {
	dsn := os.Getenv("WARMBLY_TEST_DB")
	if dsn == "" {
		t.Skip("WARMBLY_TEST_DB not set")
	}
	ctx := context.Background()
	handle, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(handle.Pool.Close)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := handle.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := handle.QueryRow(ctx, query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// callerA belongs to workspace A only; ownerB owns workspace B.
	callerA, ownerB := uuid.New(), uuid.New()
	orgA, orgB := uuid.New(), uuid.New()
	campaignA, campaignB, stepA, stepB := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	attA, attB, varA, varB := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, u := range []uuid.UUID{callerA, ownerB} {
		exec(`INSERT INTO users (id, email, first_name, last_name) VALUES ($1, $2, 'Tenant', 'Children')`, u, u.String()+"@test.local")
	}
	t.Cleanup(func() {
		exec(`DELETE FROM campaigns WHERE organization_id = ANY($1)`, []uuid.UUID{orgA, orgB})
		exec(`DELETE FROM organizations WHERE id = ANY($1)`, []uuid.UUID{orgA, orgB})
		exec(`DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{callerA, ownerB})
	})
	all := int64(models.PermViewCampaigns | models.PermManageCampaigns | models.PermManageSettings)
	for _, o := range []struct{ org, owner uuid.UUID }{{orgA, callerA}, {orgB, ownerB}} {
		exec(`INSERT INTO organizations (id, name, slug, owner_user_id) VALUES ($1, 'Tenant', $2, $3)`, o.org, o.org.String(), o.owner)
		exec(`INSERT INTO organization_members (organization_id, user_id, role, permissions, accepted_at) VALUES ($1, $2, 'owner', $3, NOW())`, o.org, o.owner, all)
	}
	for _, c := range []struct{ id, org, user, step uuid.UUID }{{campaignA, orgA, callerA, stepA}, {campaignB, orgB, ownerB, stepB}} {
		exec(`INSERT INTO campaigns (id, user_id, organization_id, name, description, days, updated_at, created_at) VALUES ($1, $2, $3, 'C', '', 62, NOW(), NOW())`, c.id, c.user, c.org)
		exec(`INSERT INTO sequences (id, campaign_id, organization_id, name, subject, body_plain, body_html, position) VALUES ($1, $2, $3, 'S', 'Hello', 'Body', '<div></div>', 1)`, c.step, c.id, c.org)
	}
	for _, a := range []struct{ id, campaign, user uuid.UUID }{{attA, campaignA, callerA}, {attB, campaignB, ownerB}} {
		exec(`INSERT INTO campaign_attachments (id, campaign_id, user_id, filename, size, mime_type, s3_key) VALUES ($1, $2, $3, 'f.pdf', 3, 'application/pdf', $4)`, a.id, a.campaign, a.user, models.AttachmentObjectKey(a.campaign, "f.pdf"))
	}
	for _, v := range []struct{ id, campaign uuid.UUID }{{varA, campaignA}, {varB, campaignB}} {
		exec(`INSERT INTO campaign_ab_variants (id, campaign_id, name, subject) VALUES ($1, $2, 'V', 'original')`, v.id, v.campaign)
	}

	store, err := storage.NewFilesystem(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	campaignRepo := repository.NewCampaignRepostory(handle)
	h := &Handler{
		CampaignService: campaign.NewService(campaignRepo, nil, nil, repository.NewCampaignLogRepository(handle), nil, nil, nil, nil, nil),
		AdvancedService: advanced.NewService(repository.NewAdvancedOutreachRepository(handle.Pool), campaignRepo, nil, nil, nil, nil, nil, nil, nil, nil, nil),
		AttachmentRepo:  repository.NewAttachmentRepository(handle),
		Storage:         store,
		AuditService:    audit.NewNoOpService(),
	}
	m := &middleware.Handler{OrganizationService: organization.NewService(repository.NewOrganizationRepository(handle.Pool), nil, nil, nil, nil)}
	read, write := models.APIPermReadCampaigns, models.APIPermWriteCampaigns
	routes := []struct {
		method, path string
		handler      gin.HandlerFunc
		orgPerm      models.OrganizationPermission
		apiPerm      uint64
	}{
		{"GET", "/campaigns/:id/advanced", h.GetCampaignAdvancedSettings, models.PermViewCampaigns, read},
		{"PATCH", "/campaigns/:id/advanced", h.UpdateCampaignAdvancedSettings, models.PermManageSettings, write},
		{"GET", "/campaigns/:id/ab-variants", h.ListCampaignABVariants, models.PermViewCampaigns, read},
		{"POST", "/campaigns/:id/ab-variants", h.CreateCampaignABVariant, models.PermManageSettings, write},
		{"PATCH", "/campaigns/:id/ab-variants/:variantId", h.UpdateCampaignABVariant, models.PermManageSettings, write},
		{"DELETE", "/campaigns/:id/ab-variants/:variantId", h.DeleteCampaignABVariant, models.PermManageSettings, write},
		{"GET", "/campaigns/:id/attachments", h.ListCampaignAttachments, models.PermViewCampaigns, read},
		{"POST", "/campaigns/:id/attachments", h.UploadCampaignAttachment, models.PermManageCampaigns, write},
		{"DELETE", "/campaigns/:id/attachments/:attachmentId", h.DeleteCampaignAttachment, models.PermManageCampaigns, write},
	}
	gin.SetMode(gin.TestMode)
	serve := func(t *testing.T, apiKey bool, method, path, contentType string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set(middleware.UserIDKey, callerA.String())
			c.Set(middleware.OrganizationIDKey, orgA)
			if apiKey {
				c.Set(middleware.AuthTypeKey, middleware.AuthTypeAPIKey)
				c.Set(middleware.APIKeyPermissionsKey, read|write)
			}
		})
		for _, r := range routes {
			router.Handle(r.method, r.path, m.RequireAccess(r.orgPerm, r.apiPerm), r.handler)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	upload := func(stepID string) (string, []byte) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		if stepID != "" {
			_ = w.WriteField("step_id", stepID)
		}
		fw, _ := w.CreateFormFile("file", "note.txt")
		_, _ = fw.Write([]byte("hi"))
		_ = w.Close()
		return w.FormDataContentType(), buf.Bytes()
	}

	cB, cA := "/campaigns/"+campaignB.String(), "/campaigns/"+campaignA.String()
	for _, caller := range []struct {
		name   string
		apiKey bool
	}{{"session", false}, {"api_key", true}} {
		t.Run(caller.name, func(t *testing.T) {
			do := func(t *testing.T, method, path, body string, want int) *httptest.ResponseRecorder {
				t.Helper()
				rec := serve(t, caller.apiKey, method, path, "application/json", []byte(body))
				if rec.Code != want {
					t.Fatalf("%s %s: status %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
				}
				return rec
			}
			// A campaign id from workspace B answers not found on every child route.
			for _, r := range []struct{ name, method, path, body string }{
				{"read advanced settings", "GET", cB + "/advanced", ""},
				{"write advanced settings", "PATCH", cB + "/advanced", `{"settings":{}}`},
				{"list variants", "GET", cB + "/ab-variants", ""},
				{"create variant", "POST", cB + "/ab-variants", `{"name":"A wrote this"}`},
				{"update variant", "PATCH", cB + "/ab-variants/" + varB.String(), `{"subject":"A wrote this"}`},
				{"delete variant", "DELETE", cB + "/ab-variants/" + varB.String(), ""},
				{"list attachments", "GET", cB + "/attachments", ""},
				{"delete attachment", "DELETE", cB + "/attachments/" + attB.String(), ""},
			} {
				t.Run("B's campaign: "+r.name, func(t *testing.T) {
					do(t, r.method, r.path, r.body, 404)
				})
			}
			t.Run("B's campaign: upload attachment", func(t *testing.T) {
				ct, body := upload("")
				if rec := serve(t, caller.apiKey, "POST", cB+"/attachments", ct, body); rec.Code != 404 {
					t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body.String())
				}
			})
			// A child id from workspace B is refused through workspace A's own campaign.
			t.Run("B's attachment via A's campaign", func(t *testing.T) {
				do(t, "DELETE", cA+"/attachments/"+attB.String(), "", 404)
			})
			t.Run("B's step on an upload to A's campaign", func(t *testing.T) {
				ct, body := upload(stepB.String())
				if rec := serve(t, caller.apiKey, "POST", cA+"/attachments", ct, body); rec.Code != 404 {
					t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body.String())
				}
			})
			t.Run("B's step on a variant in A's campaign", func(t *testing.T) {
				do(t, "POST", cA+"/ab-variants", `{"name":"x","step_id":"`+stepB.String()+`"}`, 400)
			})
			// B's variant id through A's campaign is answered exactly as an id that does not exist.
			for _, method := range []string{"PATCH", "DELETE"} {
				t.Run("B's variant via A's campaign "+method, func(t *testing.T) {
					foreign := serve(t, caller.apiKey, method, cA+"/ab-variants/"+varB.String(), "application/json", []byte(`{"subject":"A wrote this"}`))
					missing := serve(t, caller.apiKey, method, cA+"/ab-variants/"+uuid.NewString(), "application/json", []byte(`{"subject":"A wrote this"}`))
					if foreign.Code < 400 || foreign.Code != missing.Code {
						t.Fatalf("foreign variant status %d, missing variant status %d", foreign.Code, missing.Code)
					}
				})
			}
			// A's own lists carry only A's rows.
			t.Run("A lists only its own attachments", func(t *testing.T) {
				var out struct{ Data []struct{ ID uuid.UUID } }
				if err := json.Unmarshal(do(t, "GET", cA+"/attachments", "", 200).Body.Bytes(), &out); err != nil {
					t.Fatal(err)
				}
				if len(out.Data) != 1 || out.Data[0].ID != attA {
					t.Fatalf("attachments = %+v, want only A's", out.Data)
				}
			})
			t.Run("A lists only its own variants", func(t *testing.T) {
				var out struct{ Data []models.CampaignABVariant }
				if err := json.Unmarshal(do(t, "GET", cA+"/ab-variants", "", 200).Body.Bytes(), &out); err != nil {
					t.Fatal(err)
				}
				if len(out.Data) != 1 || out.Data[0].ID != varA {
					t.Fatalf("variants = %+v, want only A's", out.Data)
				}
			})
		})
	}

	t.Run("B's rows are untouched", func(t *testing.T) {
		if n := count(`SELECT COUNT(*) FROM campaign_attachments WHERE campaign_id = $1`, campaignB); n != 1 {
			t.Fatalf("B has %d attachments, want 1", n)
		}
		if n := count(`SELECT COUNT(*) FROM campaign_attachments WHERE id = $1`, attB); n != 1 {
			t.Fatal("B's attachment is gone")
		}
		if n := count(`SELECT COUNT(*) FROM campaign_ab_variants WHERE campaign_id = $1`, campaignB); n != 1 {
			t.Fatalf("B has %d variants, want 1", n)
		}
		if n := count(`SELECT COUNT(*) FROM campaign_ab_variants WHERE id = $1 AND subject = 'original'`, varB); n != 1 {
			t.Fatal("B's variant was changed")
		}
		if n := count(`SELECT COUNT(*) FROM campaign_advanced_settings WHERE campaign_id = $1`, campaignB); n != 0 {
			t.Fatal("A wrote advanced settings onto B's campaign")
		}
		if n := count(`SELECT COUNT(*) FROM campaign_attachments WHERE campaign_id = $1`, campaignA); n != 1 {
			t.Fatalf("A has %d attachments, want 1 (a refused upload must not land)", n)
		}
	})
}
