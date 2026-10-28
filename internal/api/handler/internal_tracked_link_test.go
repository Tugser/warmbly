package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/customhost"
	"github.com/warmbly/warmbly/internal/repository"
)

type stubTrackedLinks struct {
	repository.TrackedLinkRepository
	link *repository.TrackedLink
}

func (s stubTrackedLinks) GetByID(_ context.Context, id uuid.UUID) (*repository.TrackedLink, error) {
	if s.link == nil || s.link.ID != id {
		return nil, nil
	}
	return s.link, nil
}

type stubOwners map[string][]uuid.UUID

func (s stubOwners) Owners(_ context.Context, host string) ([]uuid.UUID, error) { return s[host], nil }

func TestInternalTrackedLinkHonoursCustomHostOwner(t *testing.T) {
	t.Setenv("TRACKING_DOMAIN", "track.warmbly.test")
	gin.SetMode(gin.TestMode)
	acme, other := uuid.New(), uuid.New()
	acmeLink := &repository.TrackedLink{ID: uuid.New(), Destination: "https://acme.test/", OrganizationID: &acme}
	otherLink := &repository.TrackedLink{ID: uuid.New(), Destination: "https://other.test/", OrganizationID: &other}
	orphan := &repository.TrackedLink{ID: uuid.New(), Destination: "https://gone.test/"}
	hosts := customhost.New(stubOwners{"link.acme.test": {acme}}, nil, nil)

	cases := []struct {
		name string
		link *repository.TrackedLink
		host string
		want int
	}{
		{"own link on own host", acmeLink, "link.acme.test", http.StatusOK},
		{"another workspace's link on a custom host", otherLink, "link.acme.test", http.StatusNotFound},
		{"unattributable link on a custom host", orphan, "link.acme.test", http.StatusNotFound},
		{"any link on the platform host", otherLink, "track.warmbly.test", http.StatusOK},
		{"any link on an unclaimed host", otherLink, "unclaimed.test", http.StatusOK},
		{"any link when the caller sends no host", otherLink, "", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{TrackedLinks: stubTrackedLinks{link: tc.link}, CustomHosts: hosts}
			r := gin.New()
			r.GET("/api/v1/internal/tracked-links/:id", h.InternalGetTrackedLink)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/internal/tracked-links/"+tc.link.ID.String()+"?host="+tc.host, nil))
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d", w.Code, tc.want)
			}
		})
	}
}
