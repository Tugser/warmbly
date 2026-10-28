package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	stripego "github.com/stripe/stripe-go/v86"
	"github.com/warmbly/warmbly/internal/api/middleware"
	"github.com/warmbly/warmbly/internal/app/planoffer"
	"github.com/warmbly/warmbly/internal/app/stripe"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

type refusingOffers struct{ planoffer.Service }

func (refusingOffers) AuthorizePrice(context.Context, uuid.UUID, string) *errx.Error {
	return planoffer.ErrOfferRequired
}

func (refusingOffers) AuthorizePlanID(context.Context, uuid.UUID, uuid.UUID) *errx.Error {
	return planoffer.ErrOfferRequired
}

type untouchedStripe struct {
	stripe.StripeService
	t *testing.T
}

func (s untouchedStripe) CreateCheckoutSession(context.Context, uuid.UUID, uuid.UUID, string, string, string, string) (*stripego.CheckoutSession, *errx.Error) {
	s.t.Fatal("a refused plan reached Stripe checkout")
	return nil, nil
}

func (s untouchedStripe) ChangePlan(context.Context, uuid.UUID, uuid.UUID, string, string, string) (*stripego.Subscription, *errx.Error) {
	s.t.Fatal("a refused plan reached a Stripe plan change")
	return nil, nil
}

// The generic checkout and plan change sell a private plan only with an open offer.
func TestGenericCheckoutRefusesAPlanWithoutAnOffer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{PlanOfferService: refusingOffers{}, StripeService: untouchedStripe{t: t}}
	org := uuid.New()

	for name, tc := range map[string]struct {
		handler gin.HandlerFunc
		body    string
	}{
		"checkout":    {h.CreateCheckoutSession, `{"price_id":"price_private","success_url":"/a","cancel_url":"/b"}`},
		"change-plan": {h.ChangePlan, `{"plan_id":"` + models.PartnershipPlanID.String() + `"}`},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("user_id", uuid.New().String())
		c.Set(middleware.OrganizationIDKey, org)
		tc.handler(c)

		var resp struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if w.Code != http.StatusForbidden || resp.Code != "plan_offer_required" {
			t.Errorf("%s: status %d code %q, want 403 plan_offer_required", name, w.Code, resp.Code)
		}
	}
}

func TestOfferCheckoutSessionEndsWithTheOffer(t *testing.T) {
	now := time.Now()
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	for name, tc := range map[string]struct {
		expires *time.Time
		want    time.Time
	}{
		"open-ended":          {nil, now.Add(offerCheckoutMaxLife)},
		"ends in a week":      {at(7 * 24 * time.Hour), now.Add(offerCheckoutMaxLife)},
		"ends in three hours": {at(3 * time.Hour), now.Add(3 * time.Hour)},
		"ends in ten minutes": {at(10 * time.Minute), now.Add(offerCheckoutMinLife)},
	} {
		got := offerCheckoutExpiry(&models.PlanOffer{ExpiresAt: tc.expires}, now)
		if !got.Equal(tc.want) {
			t.Errorf("%s: session expires %v, want %v", name, got.Sub(now), tc.want.Sub(now))
		}
	}
}

func TestOfferYearlyNeedsTheYearlyAmount(t *testing.T) {
	price := "price_year"
	plan := &models.Plan{StripePriceIDYearly: &price}
	if offerPriceFor(plan, "year") != "" {
		t.Error("a yearly Stripe price without price_yearly must not be sold")
	}
	amount := float32(900)
	plan.PriceYearly = &amount
	if offerPriceFor(plan, "year") != price {
		t.Error("a fully priced yearly period must be sold")
	}
}
