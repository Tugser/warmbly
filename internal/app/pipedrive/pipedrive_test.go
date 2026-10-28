package pipedrive

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/integration"
	"github.com/warmbly/warmbly/internal/models"
)

func TestVerifyWebhookPassword(t *testing.T) {
	s := New(Deps{ClientSecret: "secret", PublicURL: "https://api.example.com"})
	conn := uuid.New()
	pass := s.hookPassword(conn)
	if !s.VerifyWebhook(conn, hookUser, pass) {
		t.Fatal("the derived password must verify")
	}
	if s.VerifyWebhook(uuid.New(), hookUser, pass) {
		t.Fatal("a password is only valid for its own connection")
	}
	if s.VerifyWebhook(conn, "someone", pass) || s.VerifyWebhook(conn, hookUser, "") {
		t.Fatal("wrong user or empty password must not verify")
	}
	if New(Deps{}).VerifyWebhook(conn, hookUser, pass) {
		t.Fatal("without a client secret nothing verifies")
	}
}

func TestHookURLNeedsHTTPS(t *testing.T) {
	conn := uuid.New()
	if u := New(Deps{ClientSecret: "x", PublicURL: "http://localhost:8080"}).hookURL(conn); u != "" {
		t.Fatalf("a non-HTTPS backend registers no webhook, got %q", u)
	}
	u := New(Deps{ClientSecret: "x", PublicURL: "https://api.example.com/"}).hookURL(conn)
	if u != "https://api.example.com"+WebhookPath+conn.String() {
		t.Fatalf("unexpected webhook url %q", u)
	}
}

func TestWebhookEventIDs(t *testing.T) {
	for _, body := range []string{
		`{"meta":{"action":"change","entity":"deal","entity_id":"42"}}`,
		`{"meta":{"action":"updated","object":"deal","id":42}}`,
	} {
		var ev webhookEvent
		if err := json.Unmarshal([]byte(body), &ev); err != nil {
			t.Fatal(err)
		}
		got := rawID(ev.Meta.EntityID)
		if got == "" {
			got = rawID(ev.Meta.ID)
		}
		if got != "42" || firstNonEmpty(ev.Meta.Entity, ev.Meta.Object) != "deal" {
			t.Fatalf("%s: read id %q", body, got)
		}
	}
	if rawID(json.RawMessage(`"abc"`)) != "" || rawID(nil) != "" {
		t.Fatal("a non-numeric id is no id")
	}
}

func TestDueDates(t *testing.T) {
	due := time.Date(2026, 3, 4, 15, 30, 0, 0, time.UTC)
	body := map[string]any{}
	setDue(body, &due)
	if body["due_date"] != "2026-03-04" || body["due_time"] != "15:30" {
		t.Fatalf("unexpected due fields %v", body)
	}
	back := dueAt("2026-03-04", "15:30")
	if back == nil || !back.Equal(due) {
		t.Fatalf("round trip lost the time: %v", back)
	}
	allDay := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	body = map[string]any{}
	setDue(body, &allDay)
	if _, ok := body["due_time"]; ok {
		t.Fatal("a midnight due date is a whole day, with no time")
	}
	if parseTime("2026-03-04 10:11:12") == nil || parseTime("2026-03-04T10:11:12Z") == nil || parseTime("") != nil {
		t.Fatal("v1 and v2 timestamps must both parse")
	}
}

func TestFieldValue(t *testing.T) {
	f := &Field{FieldType: "enum", Options: []FieldOption{{ID: 7, Label: "Hot"}, {ID: 8, Label: "Cold"}}}
	if fieldValue(float64(7), f) != "Hot" {
		t.Fatal("an option id reads as its label")
	}
	set := &Field{FieldType: "set", Options: f.Options}
	if fieldValue([]any{float64(7), float64(8)}, set) != "Hot, Cold" {
		t.Fatal("a set reads as its labels")
	}
	if fieldValue(map[string]any{"value": float64(12.5), "currency": "EUR"}, nil) != "12.5 EUR" {
		t.Fatal("money reads with its currency")
	}
	if fieldValue(nil, nil) != "" || fieldValue(true, nil) != "Yes" {
		t.Fatal("empty and boolean values")
	}
}

func TestPersonEmailAndLabels(t *testing.T) {
	p := &Person{Emails: []ContactPoint{{Value: "other@x.com"}, {Value: " Main@X.com ", Primary: true}}}
	if p.Email() != "main@x.com" {
		t.Fatalf("the primary address wins, got %q", p.Email())
	}
	prev := &models.CRMContactRecord{Properties: map[string]string{"label_ids": "1,2"}}
	rec := &models.CRMContactRecord{Properties: map[string]string{"label_ids": "2,3"}}
	if got := newLabels(prev, rec); len(got) != 1 || got[0] != "3" {
		t.Fatalf("only the added label is new, got %v", got)
	}
	if !isEmailLog(&Activity{Type: "email", Done: true}) || isEmailLog(&Activity{Type: "email"}) || isEmailLog(&Activity{Type: "call", Done: true}) {
		t.Fatal("only a done email activity is a log")
	}
	if websiteDomain("https://www.acme.io/about") != "acme.io" {
		t.Fatal("a website reduces to its host")
	}
}

func TestClientPagesAndRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if n == 1 {
			w.Header().Set("X-RateLimit-Reset", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_, _ = w.Write([]byte(`{"success":true,"data":[{"id":1,"title":"A"}],"additional_data":{"next_cursor":"c2"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":[{"id":2,"title":"B"}],"additional_data":{"next_cursor":null}}`))
	}))
	defer srv.Close()
	c := NewClient("test", srv.URL, func(context.Context) (string, error) { return "tok", nil }, nil)
	deals, err := c.Deals(context.Background(), url.Values{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(deals) != 2 || deals[1].Title != "B" {
		t.Fatalf("expected both pages, got %+v", deals)
	}
	bad := NewClient("test", srv.URL, func(context.Context) (string, error) { return "nope", nil }, nil)
	_, err = bad.GetDeal(context.Background(), 1)
	ae, ok := AsAPIError(err)
	if !ok || !ae.AuthProblem() || ae.Retryable() {
		t.Fatalf("a refused token is an auth problem, got %v", err)
	}
}

func TestAPIDomainGuard(t *testing.T) {
	for _, ok := range []string{"https://acme.pipedrive.com", "https://ACME.pipedrive.com/"} {
		if d, err := integration.PipedriveAPIDomain(ok); err != nil || !strings.HasPrefix(d, "https://acme.") {
			t.Fatalf("%s should be accepted, got %q %v", ok, d, err)
		}
	}
	for _, bad := range []string{"http://acme.pipedrive.com", "https://evil.com", "https://a.b.pipedrive.com",
		"https://pipedrive.com.evil.com", "https://user@acme.pipedrive.com", "https://acme.pipedrive.com:8443"} {
		if _, err := integration.PipedriveAPIDomain(bad); err == nil {
			t.Fatalf("%s must be refused: the token is sent there", bad)
		}
	}
}

func TestConfigBelongsTo(t *testing.T) {
	legacy := models.DefaultCRMProviderConfig()
	if !legacy.BelongsTo(models.CRMProviderHubSpot) || legacy.BelongsTo(models.CRMProviderPipedrive) {
		t.Fatal("a config written before Pipedrive is HubSpot's")
	}
	pd := models.DefaultCRMProviderConfigFor(models.CRMProviderPipedrive)
	pd.For = models.CRMProviderPipedrive
	if err := pd.Validate(); err != nil {
		t.Fatalf("the Pipedrive defaults must validate: %v", err)
	}
	if pd.FieldMap["company"] != keyOrgName {
		t.Fatal("company maps to the person's organization")
	}
}

func TestVerifyAppToken(t *testing.T) {
	s := New(Deps{ClientSecret: "secret"})
	sign := func(method jwt.SigningMethod, key any, claims jwt.MapClaims) string {
		tok, err := jwt.NewWithClaims(method, claims).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	call := AppCall{CompanyID: "77", UserID: "12", Resource: "person", RecordID: "3"}
	now := float64(time.Now().Unix())
	good := sign(jwt.SigningMethodHS256, []byte("secret"), jwt.MapClaims{"userId": float64(12), "companyId": "77", "iat": now})
	if !s.VerifyAppToken(good, call) {
		t.Fatal("a token Pipedrive signed for this user and company verifies")
	}
	if s.VerifyAppToken(good, AppCall{CompanyID: "77", UserID: "13"}) {
		t.Fatal("a token names one user; the query cannot swap in another")
	}
	if s.VerifyAppToken(sign(jwt.SigningMethodHS256, []byte("other"), jwt.MapClaims{"userId": "12", "companyId": "77", "iat": now}), call) {
		t.Fatal("a token signed with another secret is refused")
	}
	if s.VerifyAppToken(sign(jwt.SigningMethodHS512, []byte("secret"), jwt.MapClaims{"userId": "12", "companyId": "77", "iat": now}), call) {
		t.Fatal("only HS256 is accepted")
	}
	expired := sign(jwt.SigningMethodHS256, []byte("secret"), jwt.MapClaims{"userId": "12", "companyId": "77", "exp": float64(time.Now().Add(-time.Hour).Unix())})
	if s.VerifyAppToken(expired, call) {
		t.Fatal("an expired token is refused")
	}
	if New(Deps{}).VerifyAppToken(good, call) {
		t.Fatal("without a client secret nothing verifies")
	}

	at := func(d time.Duration) float64 { return float64(time.Now().Add(d).Unix()) }
	lifetimes := []struct {
		name   string
		claims jwt.MapClaims
		ok     bool
	}{
		{"neither iat nor exp", jwt.MapClaims{}, false},
		{"stale iat", jwt.MapClaims{"iat": at(-time.Hour)}, false},
		{"iat in the future", jwt.MapClaims{"iat": at(time.Hour)}, false},
		{"stale iat with a live exp", jwt.MapClaims{"iat": at(-time.Hour), "exp": at(time.Hour)}, false},
		{"exp within a day", jwt.MapClaims{"exp": at(time.Hour)}, true},
		{"exp beyond a day", jwt.MapClaims{"exp": at(48 * time.Hour)}, false},
	}
	for _, tc := range lifetimes {
		tc.claims["userId"], tc.claims["companyId"] = "12", "77"
		if got := s.VerifyAppToken(sign(jwt.SigningMethodHS256, []byte("secret"), tc.claims), call); got != tc.ok {
			t.Errorf("%s: verified = %v, want %v", tc.name, got, tc.ok)
		}
	}
}
