package template

import (
	"context"
	"html"
	"strings"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type TemplateService interface {
	Create(ctx context.Context, orgID, userID uuid.UUID, data *models.CreateReplyTemplate) (*models.ReplyTemplate, *errx.Error)
	GetByID(ctx context.Context, orgID, templateID uuid.UUID) (*models.ReplyTemplate, *errx.Error)
	List(ctx context.Context, orgID uuid.UUID, search string) ([]models.ReplyTemplate, *errx.Error)
	Update(ctx context.Context, orgID, templateID uuid.UUID, data *models.UpdateReplyTemplate) (*models.ReplyTemplate, *errx.Error)
	Delete(ctx context.Context, orgID, templateID uuid.UUID) *errx.Error
	Duplicate(ctx context.Context, orgID, userID, templateID uuid.UUID) (*models.ReplyTemplate, *errx.Error)
	Reorder(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) *errx.Error
	Render(ctx context.Context, orgID, templateID uuid.UUID, vars map[string]string) (*models.RenderedReplyTemplate, *errx.Error)
}

type templateService struct {
	repo repository.TemplateRepository
}

func NewService(repo repository.TemplateRepository) TemplateService {
	return &templateService{repo: repo}
}

func (s *templateService) Create(ctx context.Context, orgID, userID uuid.UUID, data *models.CreateReplyTemplate) (*models.ReplyTemplate, *errx.Error) {
	data.Name = strings.TrimSpace(data.Name)
	if data.Name == "" {
		return nil, errx.New(errx.BadRequest, "name is required")
	}
	if len(data.Name) > 255 {
		return nil, errx.New(errx.BadRequest, "name must be at most 255 characters")
	}

	t, err := s.repo.Create(ctx, orgID, userID, data)
	if err != nil {
		return nil, errx.InternalError()
	}

	return t, nil
}

func (s *templateService) GetByID(ctx context.Context, orgID, templateID uuid.UUID) (*models.ReplyTemplate, *errx.Error) {
	t, err := s.repo.GetByID(ctx, orgID, templateID)
	if err != nil {
		return nil, errx.InternalError()
	}
	if t == nil {
		return nil, errx.ErrNotFound
	}

	return t, nil
}

func (s *templateService) List(ctx context.Context, orgID uuid.UUID, search string) ([]models.ReplyTemplate, *errx.Error) {
	templates, err := s.repo.List(ctx, orgID, search)
	if err != nil {
		return nil, errx.InternalError()
	}

	if templates == nil {
		templates = []models.ReplyTemplate{}
	}

	return templates, nil
}

func (s *templateService) Update(ctx context.Context, orgID, templateID uuid.UUID, data *models.UpdateReplyTemplate) (*models.ReplyTemplate, *errx.Error) {
	if data.Name != nil {
		trimmed := strings.TrimSpace(*data.Name)
		if trimmed == "" {
			return nil, errx.New(errx.BadRequest, "name cannot be empty")
		}
		if len(trimmed) > 255 {
			return nil, errx.New(errx.BadRequest, "name must be at most 255 characters")
		}
		data.Name = &trimmed
	}

	t, err := s.repo.Update(ctx, orgID, templateID, data)
	if err != nil {
		return nil, errx.InternalError()
	}
	if t == nil {
		return nil, errx.ErrNotFound
	}

	return t, nil
}

func (s *templateService) Delete(ctx context.Context, orgID, templateID uuid.UUID) *errx.Error {
	if err := s.repo.Delete(ctx, orgID, templateID); err != nil {
		return errx.InternalError()
	}

	return nil
}

func (s *templateService) Duplicate(ctx context.Context, orgID, userID, templateID uuid.UUID) (*models.ReplyTemplate, *errx.Error) {
	t, err := s.repo.Duplicate(ctx, orgID, userID, templateID)
	if err != nil {
		return nil, errx.InternalError()
	}
	if t == nil {
		return nil, errx.ErrNotFound
	}

	return t, nil
}

func (s *templateService) Reorder(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) *errx.Error {
	if len(ids) == 0 {
		return errx.New(errx.BadRequest, "ids cannot be empty")
	}

	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			return errx.New(errx.BadRequest, "duplicate id in reorder list")
		}
		seen[id] = struct{}{}
	}

	if err := s.repo.Reorder(ctx, orgID, ids); err != nil {
		return errx.InternalError()
	}

	return nil
}

// Render expands {{.Key}} placeholders in subject + body fields against the
// provided variable map. Missing keys are replaced with an empty string,
// matching the cold-email render semantics in internal/tasks/template.go.
func (s *templateService) Render(ctx context.Context, orgID, templateID uuid.UUID, vars map[string]string) (*models.RenderedReplyTemplate, *errx.Error) {
	t, xerr := s.GetByID(ctx, orgID, templateID)
	if xerr != nil {
		return nil, xerr
	}

	return &models.RenderedReplyTemplate{
		Subject:   RenderString(t.Subject, vars),
		BodyHTML:  RenderHTMLString(t.BodyHTML, vars),
		BodyPlain: RenderString(t.BodyPlain, vars),
	}, nil
}

// RenderString replaces {{.Key}} placeholders in s with values from vars.
// Unknown keys are dropped (rendered as empty string) so previews don't
// leak the placeholder syntax to recipients.
func RenderString(s string, vars map[string]string) string { return renderVars(s, vars, false) }

// RenderHTMLString is RenderString for an HTML body: every value is HTML-escaped.
func RenderHTMLString(s string, vars map[string]string) string { return renderVars(s, vars, true) }

// renderVars replaces each {{.Key}} once, left to right; an inserted value is never rescanned.
func renderVars(s string, vars map[string]string, escape bool) string {
	var b strings.Builder
	for {
		start := strings.Index(s, "{{.")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "}}")
		if end < 0 {
			break
		}
		v := vars[s[start+3:start+end]]
		if escape {
			v = html.EscapeString(v)
		}
		b.WriteString(s[:start])
		b.WriteString(v)
		s = s[start+end+2:]
	}
	b.WriteString(s)
	return b.String()
}
