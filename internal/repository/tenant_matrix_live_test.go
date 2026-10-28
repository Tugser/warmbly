package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

// Every read, update and delete of an organization's row answers nothing to another organization.
func TestLiveTenantMatrixRefusesAnotherOrganization(t *testing.T) {
	handle, pool := liveContactDB(t)
	ctx := context.Background()
	owner, orgA, orgB, contactB := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	exec(`INSERT INTO users (id, first_name, last_name, email) VALUES ($1, 'Tenant', 'Matrix', $2)`, owner, owner.String()+"@example.test")
	exec(`INSERT INTO organizations (id, name, owner_user_id) VALUES ($1, 'A', $3), ($2, 'B', $3)`, orgA, orgB, owner)
	exec(`INSERT INTO organization_members (organization_id, user_id, role, permissions, accepted_at)
	      VALUES ($1, $3, 'owner', -1, NOW()), ($2, $3, 'owner', -1, NOW())`, orgA, orgB, owner)
	exec(`INSERT INTO contacts (id, user_id, organization_id, email, first_name, last_name, company, phone, custom_fields)
	      VALUES ($1, $2, $3, $4, 'B', 'Contact', '', '', '{}')`, contactB, owner, orgB, contactB.String()+"@example.test")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id = ANY($1)`, []uuid.UUID{orgA, orgB})
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, owner)
	})

	crm := NewCRMRepository(pool)
	note, err := crm.CreateNote(ctx, orgB, contactB, owner, "B only")
	if err != nil {
		t.Fatal(err)
	}
	pipe, err := crm.CreatePipeline(ctx, orgB, &models.CreatePipeline{Name: "B", Stages: []models.CreatePipelineStage{{Name: "Open", Color: "#000000"}}})
	if err != nil {
		t.Fatal(err)
	}
	pipe, err = crm.GetPipeline(ctx, orgB, pipe.ID)
	if err != nil || pipe == nil || len(pipe.Stages) == 0 {
		t.Fatalf("pipeline B: %v %v", pipe, err)
	}
	deal, err := crm.CreateDeal(ctx, orgB, &models.CreateDeal{PipelineID: pipe.ID, StageID: pipe.Stages[0].ID, Name: "B deal", ContactID: &contactB})
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := NewTemplateRepository(pool).Create(ctx, orgB, owner, &models.CreateReplyTemplate{Name: "B"})
	if err != nil {
		t.Fatal(err)
	}
	team, err := NewTeamRepository(pool).CreateTeam(ctx, orgB, &models.CreateTeam{Name: "B"})
	if err != nil {
		t.Fatal(err)
	}
	seg, xerr := NewSegmentRepository(handle).Create(ctx, orgB, &owner, &models.Segment{Name: "B", Color: "#0284c7", Match: models.SegmentMatchAll, Conditions: []models.SegmentCondition{{Field: "company", Operator: "equals", Value: "b"}}})
	if xerr != nil {
		t.Fatal(xerr)
	}
	hooks := NewWebhookRepository(pool)
	hook := &models.WebhookEndpoint{ID: uuid.New()}
	exec(`INSERT INTO webhook_endpoints (id, organization_id, url, secret) VALUES ($1, $2, 'https://example.test/hook', 'fixture')`, hook.ID, orgB)
	integ := NewIntegrationRepository(pool)
	auto := &models.Automation{OrganizationID: orgB, Name: "B", TriggerEvent: "reply.received"}
	if err := integ.CreateAutomation(ctx, auto); err != nil {
		t.Fatal(err)
	}
	transfer := NewOrgTransferRepository(handle)
	export := &models.OrgExportJob{OrganizationID: orgB, RequestedBy: &owner}
	if err := transfer.CreateExportJob(ctx, export); err != nil {
		t.Fatal(err)
	}
	agents := NewAgentRepository(handle)
	session, err := agents.CreateSession(ctx, orgB, owner, "B", models.AgentSessionContext{})
	if err != nil {
		t.Fatal(err)
	}

	// Organization A names every one of B's ids.
	refusals := []struct {
		name string
		do   func() (found bool)
	}{
		{"read note", func() bool { n, _ := crm.GetNote(ctx, orgA, note.ID); return n != nil }},
		{"update note", func() bool { n, _ := crm.UpdateNote(ctx, orgA, note.ID, "A wrote this"); return n != nil }},
		{"delete note", func() bool { return crm.DeleteNote(ctx, orgA, note.ID) == nil }},
		{"read pipeline", func() bool { p, _ := crm.GetPipeline(ctx, orgA, pipe.ID); return p != nil }},
		{"rename pipeline", func() bool { p, _ := crm.UpdatePipeline(ctx, orgA, pipe.ID, "A"); return p != nil }},
		{"delete pipeline", func() bool { return crm.DeletePipeline(ctx, orgA, pipe.ID) == nil }},
		{"read deal", func() bool { d, _ := crm.GetDeal(ctx, orgA, deal.ID); return d != nil }},
		{"update deal", func() bool {
			n := "A"
			d, _ := crm.UpdateDeal(ctx, orgA, deal.ID, &models.UpdateDeal{Name: &n})
			return d != nil
		}},
		{"delete deal", func() bool { return crm.DeleteDeal(ctx, orgA, deal.ID) == nil }},
		{"deal on B's pipeline", func() bool {
			d, _ := crm.CreateDeal(ctx, orgA, &models.CreateDeal{PipelineID: pipe.ID, StageID: pipe.Stages[0].ID, Name: "A"})
			return d != nil
		}},
		{"note on B's contact", func() bool { n, _ := crm.CreateNote(ctx, orgA, contactB, owner, "A"); return n != nil }},
		{"read template", func() bool { x, _ := NewTemplateRepository(pool).GetByID(ctx, orgA, tmpl.ID); return x != nil }},
		// An idempotent delete answers success either way; what matters is that B's row survives it.
		{"delete template", func() bool {
			_ = NewTemplateRepository(pool).Delete(ctx, orgA, tmpl.ID)
			x, _ := NewTemplateRepository(pool).GetByID(ctx, orgB, tmpl.ID)
			return x == nil
		}},
		{"read team", func() bool { x, _ := NewTeamRepository(pool).GetTeam(ctx, orgA, team.ID); return x != nil }},
		{"delete team", func() bool { return NewTeamRepository(pool).DeleteTeam(ctx, orgA, team.ID) == nil }},
		{"read segment", func() bool { x, _ := NewSegmentRepository(handle).Get(ctx, orgA, seg.ID); return x != nil }},
		{"delete segment", func() bool { return NewSegmentRepository(handle).Delete(ctx, orgA, seg.ID) == nil }},
		{"read webhook", func() bool { x, _ := hooks.GetEndpoint(ctx, orgA, hook.ID); return x != nil }},
		{"delete webhook", func() bool { return hooks.DeleteEndpoint(ctx, orgA, hook.ID) == nil }},
		{"read automation", func() bool { x, _ := integ.GetAutomation(ctx, orgA, auto.ID); return x != nil }},
		{"read export archive", func() bool { x, _ := transfer.GetExportJob(ctx, orgA, export.ID); return x != nil }},
		{"delete export archive", func() bool {
			_, _ = transfer.DeleteExportJob(ctx, orgA, export.ID)
			x, _ := transfer.GetExportJob(ctx, orgB, export.ID)
			return x == nil
		}},
		{"read agent session", func() bool { x, _ := agents.GetSessionOrg(ctx, orgA, session.ID); return x != nil }},
		{"delete automation", func() bool {
			x, _ := integ.GetAutomation(ctx, orgB, auto.ID)
			return x == nil
		}},
	}
	for _, r := range refusals {
		t.Run(r.name, func(t *testing.T) {
			if r.do() {
				t.Fatalf("organization A reached organization B's row")
			}
		})
	}

	// Nothing A attempted changed B's rows.
	t.Run("B's rows are untouched", func(t *testing.T) {
		if n, _ := crm.GetNote(ctx, orgB, note.ID); n == nil || n.Content != "B only" {
			t.Fatalf("note = %+v", n)
		}
		if p, _ := crm.GetPipeline(ctx, orgB, pipe.ID); p == nil || p.Name != "B" {
			t.Fatalf("pipeline = %+v", p)
		}
		if d, _ := crm.GetDeal(ctx, orgB, deal.ID); d == nil || d.Name != "B deal" {
			t.Fatalf("deal = %+v", d)
		}
		if x, _ := NewTemplateRepository(pool).GetByID(ctx, orgB, tmpl.ID); x == nil {
			t.Fatal("template gone")
		}
		if x, _ := NewTeamRepository(pool).GetTeam(ctx, orgB, team.ID); x == nil {
			t.Fatal("team gone")
		}
		if x, _ := NewSegmentRepository(handle).Get(ctx, orgB, seg.ID); x == nil {
			t.Fatal("segment gone")
		}
		if x, _ := hooks.GetEndpoint(ctx, orgB, hook.ID); x == nil {
			t.Fatal("webhook gone")
		}
		if x, _ := integ.GetAutomation(ctx, orgB, auto.ID); x == nil {
			t.Fatal("automation gone")
		}
	})
}
