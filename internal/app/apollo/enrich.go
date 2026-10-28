package apollo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/warmbly/warmbly/internal/app/contact"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func jsonUnmarshal(raw []byte, v any) error { return json.Unmarshal(raw, v) }

// StartEnrichment queues an enrichment run over the given contacts and starts
// it in the background. Apollo charges one credit per person it matches.
func (s *Service) StartEnrichment(ctx context.Context, orgID, connID, userID uuid.UUID, ids []uuid.UUID, overwrite bool) (*models.ApolloEnrichmentRun, error) {
	if _, _, err := s.conn(ctx, orgID, connID); err != nil {
		return nil, err
	}
	seen := map[uuid.UUID]bool{}
	unique := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id != uuid.Nil && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	if len(unique) == 0 {
		return nil, errx.New(errx.BadRequest, "select at least one contact to enrich")
	}
	if len(unique) > EnrichCap {
		return nil, errx.NewWithIdentifier(errx.BadRequest, "apollo_enrich_too_many", "Enrich at most 5,000 contacts at a time")
	}
	// A run records only the workspace's own contacts.
	owned, err := s.Repo.ContactsForEnrichment(ctx, orgID, unique)
	if err != nil {
		return nil, err
	}
	unique = unique[:0]
	for _, c := range owned {
		unique = append(unique, c.ID)
	}
	if len(unique) == 0 {
		return nil, errx.New(errx.BadRequest, "select at least one contact to enrich")
	}
	uid := userID
	run := &models.ApolloEnrichmentRun{
		OrganizationID:    orgID,
		ConnectionID:      connID,
		RequestedByUserID: &uid,
		ContactIDs:        unique,
		Overwrite:         overwrite,
		Status:            models.ApolloEnrichmentQueued,
		Total:             len(unique),
	}
	if err := s.Repo.CreateEnrichmentRun(ctx, run); err != nil {
		if errors.Is(err, repository.ErrApolloSourceRefs) {
			return nil, errx.New(errx.NotFound, "Apollo connection not found")
		}
		return nil, err
	}
	go func(id uuid.UUID) {
		bg, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		s.runEnrichment(bg, id)
	}(run.ID)
	return run, nil
}

// RunPendingEnrichment picks up queued runs and runs a crashed process left behind.
func (s *Service) RunPendingEnrichment(ctx context.Context) error {
	ids, err := s.Repo.DueEnrichmentRuns(ctx, 10)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.runEnrichment(ctx, id)
	}
	return nil
}

func (s *Service) runEnrichment(ctx context.Context, id uuid.UUID) {
	run, err := s.Repo.ClaimEnrichmentRun(ctx, id)
	if err != nil || run == nil {
		if err != nil {
			log.Warn().Err(err).Str("run", id.String()).Msg("apollo: could not claim enrichment run")
		}
		return
	}
	err = s.enrich(ctx, run)
	run.Status = models.ApolloEnrichmentDone
	if err != nil {
		run.Status = models.ApolloEnrichmentError
		var xe *errx.Error
		if errors.As(err, &xe) {
			run.LastError = xe.Message
		} else {
			run.LastError = plainErr(err).Error()
		}
		log.Warn().Err(err).Str("run", id.String()).Msg("apollo: enrichment run failed")
	}
	fin, cancel := finishContext(ctx)
	defer cancel()
	if ferr := s.Repo.FinishEnrichmentRun(fin, run); ferr != nil {
		log.Warn().Err(ferr).Str("run", id.String()).Msg("apollo: could not finish enrichment run")
	}
}

// enrich walks the run's contacts from where it stopped, matching ten at a
// time and saving each chunk before moving on.
func (s *Service) enrich(ctx context.Context, run *models.ApolloEnrichmentRun) error {
	cl, c, err := s.conn(ctx, run.OrganizationID, run.ConnectionID)
	if err != nil {
		return err
	}
	actor := uuid.Nil
	if run.RequestedByUserID != nil {
		actor = *run.RequestedByUserID
	} else if c.ConnectedByUserID != nil {
		actor = *c.ConnectedByUserID
	}
	for run.Processed < len(run.ContactIDs) {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(run.Processed+enrichChunk, len(run.ContactIDs))
		chunkIDs := run.ContactIDs[run.Processed:end]
		refs, err := s.Repo.ContactsForEnrichment(ctx, run.OrganizationID, chunkIDs)
		if err != nil {
			return err
		}
		// A contact deleted since the run was queued is simply not there.
		run.NotFound += len(chunkIDs) - len(refs)

		var rows []contact.ImportRow
		var rowRefs []uuid.UUID
		for start := 0; start < len(refs); start += MatchBatchSize {
			batch := refs[start:min(start+MatchBatchSize, len(refs))]
			details := make([]MatchDetail, len(batch))
			for i, r := range batch {
				details[i] = MatchDetail{
					Email: r.Email, FirstName: r.FirstName, LastName: r.LastName,
					OrganizationName: r.Company, LinkedinURL: r.CustomFields["linkedin_url"],
				}
			}
			res, err := cl.BulkMatch(ctx, details)
			if err != nil {
				s.noteHealth(ctx, c.ID, err)
				if errors.Is(err, ErrKey) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrRateLimited) || ctx.Err() != nil {
					// Save what this chunk already matched before stopping.
					s.applyEnrichment(ctx, run, actor, rows, rowRefs)
					return err
				}
				run.Failed += len(batch)
				continue
			}
			run.CreditsConsumed += res.CreditsConsumed
			for i, r := range batch {
				p := res.Matches[i]
				if p == nil {
					run.NotFound++
					continue
				}
				cells, changed := enrichCells(r, *p, run.Overwrite)
				if !changed {
					continue
				}
				rows = append(rows, contact.ImportRow{Line: len(rows) + 1, Cells: cells})
				rowRefs = append(rowRefs, r.ID)
			}
		}
		s.applyEnrichment(ctx, run, actor, rows, rowRefs)
		run.Processed = end
		if err := s.Repo.UpdateEnrichmentProgress(ctx, run); err != nil {
			return err
		}
	}
	s.noteHealth(ctx, c.ID, nil)
	return nil
}

// applyEnrichment saves a chunk's matched fields through the contact importer,
// which never blanks a field and never changes an address.
func (s *Service) applyEnrichment(ctx context.Context, run *models.ApolloEnrichmentRun, actor uuid.UUID, rows []contact.ImportRow, refs []uuid.UUID) {
	if len(rows) == 0 {
		return
	}
	opts := &models.ContactImportCommit{
		Mapping:             importMapping(),
		Dedup:               models.ContactImportDedupUpdate,
		SkipMissingSegments: true,
		Source:              models.ContactSourceApollo,
		SourceDetail:        "Apollo enrichment",
	}
	sink := newCollectSink()
	if _, xerr := s.Contacts.RunImport(ctx, actor.String(), run.OrganizationID, rows, opts, sink, nil); xerr != nil {
		run.Failed += len(rows)
		log.Warn().Str("run", run.ID.String()).Str("reason", xerr.Message).Msg("apollo: enrichment save failed")
		return
	}
	for i := range refs {
		o, ok := sink.outcomes[i+1]
		switch {
		case !ok || o.Status == models.ContactImportRowFailed:
			run.Failed++
		default:
			run.Enriched++
		}
	}
}

// enrichCells builds an import row holding only the fields enrichment may
// write: blanks by default, every field Apollo knows with overwrite. The
// address is the row's key and stays the contact's own.
func enrichCells(r repository.ApolloContactRef, p Person, overwrite bool) ([]string, bool) {
	current := func(target models.ContactImportColumnTarget, custom string) string {
		switch target {
		case models.ContactImportTargetFirstName:
			return r.FirstName
		case models.ContactImportTargetLastName:
			return r.LastName
		case models.ContactImportTargetCompany:
			return r.Company
		case models.ContactImportTargetPhone:
			return r.Phone
		case models.ContactImportTargetCustom:
			return r.CustomFields[custom]
		}
		return ""
	}
	cells := make([]string, len(importColumns))
	changed := false
	for i, col := range importColumns {
		if col.target == models.ContactImportTargetEmail {
			cells[i] = r.Email
			continue
		}
		v := truncate(col.value(p), 500)
		if v == "" {
			continue
		}
		have := strings.TrimSpace(current(col.target, col.custom))
		if have == v || (have != "" && !overwrite) {
			continue
		}
		cells[i] = v
		changed = true
	}
	return cells, changed
}
