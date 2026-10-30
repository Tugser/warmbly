package planoffer

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type memOffers struct {
	repository.PlanOfferRepository
	rows map[[2]uuid.UUID]*models.PlanOffer
}

func (m *memOffers) GetPlanOffer(_ context.Context, org, plan uuid.UUID) (*models.PlanOffer, error) {
	return m.rows[[2]uuid.UUID{org, plan}], nil
}

func (m *memOffers) UpsertPlanOffer(_ context.Context, o *models.PlanOffer) (*models.PlanOffer, error) {
	m.rows[[2]uuid.UUID{o.OrganizationID, o.PlanID}] = o
	return o, nil
}

func (m *memOffers) ListPlanOffers(_ context.Context, org uuid.UUID) ([]*models.PlanOffer, error) {
	var out []*models.PlanOffer
	for k, o := range m.rows {
		if k[0] == org {
			out = append(out, o)
		}
	}
	return out, nil
}

type memPlans struct {
	repository.PlanRepository
	plans map[uuid.UUID]*models.Plan
}

func (m memPlans) GetByID(_ context.Context, id uuid.UUID) (*models.Plan, error) {
	return m.plans[id], nil
}

func fixture() (*service, *memOffers, uuid.UUID) {
	public := &models.Plan{ID: uuid.New(), Public: true}
	plans := memPlans{plans: map[uuid.UUID]*models.Plan{
		public.ID:                {ID: public.ID, Public: true},
		models.PartnershipPlanID: {ID: models.PartnershipPlanID},
		models.WarmupPlanID:      {ID: models.WarmupPlanID},
	}}
	offers := &memOffers{rows: map[[2]uuid.UUID]*models.PlanOffer{}}
	return &service{offers: offers, plans: plans, now: time.Now}, offers, public.ID
}

func TestAuthorizePurchaseSellsPublicPlansAndOfferedPrivateOnes(t *testing.T) {
	ctx := context.Background()
	s, offers, publicID := fixture()
	org := uuid.New()
	partnership := &models.Plan{ID: models.PartnershipPlanID}

	if xerr := s.AuthorizePurchase(ctx, org, &models.Plan{ID: publicID, Public: true}); xerr != nil {
		t.Fatalf("public plan refused: %v", xerr)
	}
	if xerr := s.AuthorizePurchase(ctx, org, partnership); xerr != ErrOfferRequired {
		t.Fatalf("private plan without an offer: got %v, want plan_offer_required", xerr)
	}
	if xerr := s.AuthorizePurchase(ctx, org, &models.Plan{ID: models.WarmupPlanID}); xerr != ErrOfferRequired {
		t.Fatalf("the Warmup plan through the generic checkout: got %v, want plan_offer_required", xerr)
	}
	if xerr := s.AuthorizePurchase(ctx, org, nil); xerr == nil {
		t.Fatal("a price matching no plan must be refused")
	}

	past := time.Now().Add(-time.Minute)
	offers.rows[[2]uuid.UUID{org, partnership.ID}] = &models.PlanOffer{OrganizationID: org, PlanID: partnership.ID, Reason: "r", ExpiresAt: &past}
	if xerr := s.AuthorizePurchase(ctx, org, partnership); xerr != ErrOfferRequired {
		t.Fatalf("expired offer: got %v, want plan_offer_required", xerr)
	}

	if _, xerr := s.GrantOffer(ctx, org, partnership.ID, uuid.New(), "partner", nil); xerr != nil {
		t.Fatalf("grant: %v", xerr)
	}
	if xerr := s.AuthorizePurchase(ctx, org, partnership); xerr != nil {
		t.Fatalf("offered plan refused: %v", xerr)
	}
	if xerr := s.AuthorizePurchase(ctx, uuid.New(), partnership); xerr != ErrOfferRequired {
		t.Fatalf("another workspace's offer must not sell the plan: got %v", xerr)
	}
}

func TestGrantOfferRefusesWhatAnOfferCannotMean(t *testing.T) {
	ctx := context.Background()
	s, _, publicID := fixture()
	org, admin := uuid.New(), uuid.New()
	past := time.Now().Add(-time.Hour)

	cases := map[string]struct {
		plan    uuid.UUID
		reason  string
		expires *time.Time
	}{
		"blank reason":    {models.PartnershipPlanID, "  ", nil},
		"expired":         {models.PartnershipPlanID, "r", &past},
		"public plan":     {publicID, "r", nil},
		"warmup plan":     {models.WarmupPlanID, "r", nil},
		"unknown plan":    {uuid.New(), "r", nil},
		"overlong reason": {models.PartnershipPlanID, string(make([]byte, 501)), nil},
	}
	for name, tc := range cases {
		if _, xerr := s.GrantOffer(ctx, org, tc.plan, admin, tc.reason, tc.expires); xerr == nil {
			t.Errorf("%s: granted, want refusal", name)
		}
	}
}

func TestActiveOffersDropsExpiredOnes(t *testing.T) {
	ctx := context.Background()
	s, offers, _ := fixture()
	org := uuid.New()
	past, future := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)
	other := uuid.New()
	offers.rows[[2]uuid.UUID{org, models.PartnershipPlanID}] = &models.PlanOffer{OrganizationID: org, PlanID: models.PartnershipPlanID, ExpiresAt: &future}
	offers.rows[[2]uuid.UUID{org, other}] = &models.PlanOffer{OrganizationID: org, PlanID: other, ExpiresAt: &past}

	active, xerr := s.ActiveOffers(ctx, org)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if len(active) != 1 || active[0].PlanID != models.PartnershipPlanID || active[0].Plan == nil {
		t.Fatalf("active offers = %+v, want only the open Partnership offer with its plan", active)
	}
}
