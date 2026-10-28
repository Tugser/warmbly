package releases

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

type memSettings struct{ release *models.FleetReleaseState }

func (m *memSettings) GetRelease(context.Context) (*models.FleetReleaseState, error) {
	return m.release, nil
}

func (m *memSettings) SetRelease(_ context.Context, s *models.FleetReleaseState) error {
	m.release = s
	return nil
}

func (m *memSettings) CompareAndSetRelease(_ context.Context, _, next *models.FleetReleaseState) (bool, error) {
	m.release = next
	return true, nil
}

func (m *memSettings) GetJoinToken(context.Context) (string, *time.Time, error) { return "", nil, nil }
func (m *memSettings) SetJoinToken(context.Context, string, time.Time) error    { return nil }

type githubStub struct{}

func (githubStub) RoundTrip(*http.Request) (*http.Response, error) {
	body := `[{"tag_name":"v2.0.0","published_at":"2026-09-29T12:00:00Z"},{"tag_name":"v1.1.0","published_at":"2026-09-28T12:00:00Z"}]`
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
}

func TestCheckGitHubSelectsNewestBackendCompatibleRelease(t *testing.T) {
	settings := &memSettings{release: &models.FleetReleaseState{Channel: models.FleetChannelStable, Tag: "v1.0.0"}}
	svc := New(Config{Enabled: true, GithubRepo: "warmbly/warmbly", BackendVersion: "v1.1.0", HTTPClient: &http.Client{Transport: githubStub{}}}, settings)
	if _, err := svc.CheckGitHub(context.Background()); err != nil {
		t.Fatal(err)
	}
	if settings.release.Tag != "v1.1.0" {
		t.Fatalf("fleet target %s, want v1.1.0", settings.release.Tag)
	}
	if svc.GetState().Channels[models.FleetChannelStable].Tag != "v2.0.0" {
		t.Fatal("dashboard must still report the actual latest release")
	}
	if _, err := svc.SetTag(context.Background(), "v2.0.0"); err == nil {
		t.Fatal("cannot pin the fleet ahead of the backend")
	}
	if settings.release.Tag != "v1.1.0" {
		t.Fatal("refused pin changed stored target")
	}
}

func TestCheckGitHubHoldsTheFleetWhenTheSchemaGateRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		gate func(context.Context, string) error
		want string
	}{
		{"refused", func(context.Context, string) error { return errors.New("incompatible") }, "v1.0.0"},
		{"accepted", func(context.Context, string) error { return nil }, "v2.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := &memSettings{release: &models.FleetReleaseState{Channel: models.FleetChannelStable, Tag: "v1.0.0"}}
			svc := New(Config{
				Enabled:    true,
				GithubRepo: "warmbly/warmbly",
				HTTPClient: &http.Client{Transport: githubStub{}},
				SchemaGate: tc.gate,
			}, settings)
			if _, err := svc.CheckGitHub(context.Background()); err != nil {
				t.Fatal(err)
			}
			if settings.release.Tag != tc.want {
				t.Fatalf("fleet target %s, want %s", settings.release.Tag, tc.want)
			}
		})
	}
}
