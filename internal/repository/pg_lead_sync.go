package repository

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/models"
)

// LeadSyncRepository owns persistence for on-demand Google Sheets -> leads
// sync sources. Every query is organization-scoped; a source is only ever
// reachable by the org that created it.
type LeadSyncRepository interface {
	Create(ctx context.Context, src *models.LeadSyncSource) error
	// List filters to the sources feeding one campaign and/or one segment;
	// both nil lists every source in the organization.
	List(ctx context.Context, orgID uuid.UUID, campaignID, segmentID *uuid.UUID) ([]models.LeadSyncSource, error)
	Get(ctx context.Context, orgID, id uuid.UUID) (*models.LeadSyncSource, error)
	Update(ctx context.Context, src *models.LeadSyncSource) error
	Delete(ctx context.Context, orgID, id uuid.UUID) error
	// SetResult records the outcome of a "Sync now" run. resultJSON is the
	// marshalled *models.ContactImportResult (nil-safe).
	SetResult(ctx context.Context, id uuid.UUID, status models.LeadSyncStatus, lastSyncedAt *time.Time, resultJSON []byte, lastErr string) error
	// TargetsInOrganization reports whether the campaign (when set) and every category id belong to the organization.
	TargetsInOrganization(ctx context.Context, orgID uuid.UUID, campaignID *uuid.UUID, categoryIDs []string) (bool, error)
}

type leadSyncRepository struct {
	db *pgxpool.Pool
}

func NewLeadSyncRepository(db *pgxpool.Pool) LeadSyncRepository {
	return &leadSyncRepository{db: db}
}

const leadSyncCols = `
	id, organization_id, created_by_user_id, provider, connection_id,
	sheet_id, COALESCE(sheet_title, ''), COALESCE(tab_title, ''), COALESCE(a1_range, ''),
	has_header, column_mapping, dedup, target_campaign_id, category_ids, segment_ids,
	subscribed_default, COALESCE(label, ''), status, last_synced_at, last_result,
	COALESCE(last_error, ''), created_at, updated_at`

func (r *leadSyncRepository) Create(ctx context.Context, src *models.LeadSyncSource) error {
	if src.ID == uuid.Nil {
		src.ID = uuid.New()
	}
	now := time.Now().UTC()
	src.CreatedAt = now
	src.UpdatedAt = now
	if src.Provider == "" {
		src.Provider = string(models.IntegrationGoogleSheets)
	}
	if src.Status == "" {
		src.Status = models.LeadSyncStatusIdle
	}

	mapping := marshalJSONDefault(src.ColumnMapping, "[]")
	cats := marshalJSONDefault(src.CategoryIDs, "[]")
	segs := marshalJSONDefault(src.SegmentIDs, "[]")

	_, err := r.db.Exec(ctx, `
		INSERT INTO lead_sync_sources (
			id, organization_id, created_by_user_id, provider, connection_id,
			sheet_id, sheet_title, tab_title, a1_range, has_header,
			column_mapping, dedup, target_campaign_id, category_ids, segment_ids,
			subscribed_default, label, status, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15,
			$16, $17, $18, $19, $19
		)`,
		src.ID, src.OrganizationID, src.CreatedByUserID, src.Provider, src.ConnectionID,
		src.SheetID, nullIfEmptyStr(src.SheetTitle), nullIfEmptyStr(src.TabTitle), nullIfEmptyStr(src.A1Range), src.HasHeader,
		mapping, string(src.Dedup), src.TargetCampaignID, cats, segs,
		src.SubscribedDefault, nullIfEmptyStr(src.Label), string(src.Status), now,
	)
	return err
}

func (r *leadSyncRepository) List(ctx context.Context, orgID uuid.UUID, campaignID, segmentID *uuid.UUID) ([]models.LeadSyncSource, error) {
	// The segment filter is a jsonb containment test against the id list the
	// importer reads, so it needs no extra column.
	var wantSegment []byte
	if segmentID != nil {
		wantSegment = []byte(`["` + segmentID.String() + `"]`)
	}
	rows, err := r.db.Query(ctx, `SELECT `+leadSyncCols+`
		FROM lead_sync_sources
		WHERE organization_id = $1
		  AND ($2::uuid IS NULL OR target_campaign_id = $2)
		  AND ($3::jsonb IS NULL OR segment_ids @> $3::jsonb)
		ORDER BY created_at DESC`, orgID, campaignID, wantSegment)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.LeadSyncSource{}
	for rows.Next() {
		var s models.LeadSyncSource
		if err := scanLeadSyncInto(rows, &s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *leadSyncRepository) Get(ctx context.Context, orgID, id uuid.UUID) (*models.LeadSyncSource, error) {
	row := r.db.QueryRow(ctx, `SELECT `+leadSyncCols+`
		FROM lead_sync_sources WHERE organization_id = $1 AND id = $2`, orgID, id)
	var s models.LeadSyncSource
	if err := scanLeadSyncInto(row, &s); err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *leadSyncRepository) Update(ctx context.Context, src *models.LeadSyncSource) error {
	now := time.Now().UTC()
	src.UpdatedAt = now
	mapping := marshalJSONDefault(src.ColumnMapping, "[]")
	cats := marshalJSONDefault(src.CategoryIDs, "[]")
	segs := marshalJSONDefault(src.SegmentIDs, "[]")

	_, err := r.db.Exec(ctx, `
		UPDATE lead_sync_sources SET
			sheet_id = $1, sheet_title = $2, tab_title = $3, a1_range = $4,
			has_header = $5, column_mapping = $6, dedup = $7, target_campaign_id = $8,
			category_ids = $9, segment_ids = $10, subscribed_default = $11, label = $12, updated_at = $13
		WHERE organization_id = $14 AND id = $15`,
		src.SheetID, nullIfEmptyStr(src.SheetTitle), nullIfEmptyStr(src.TabTitle), nullIfEmptyStr(src.A1Range),
		src.HasHeader, mapping, string(src.Dedup), src.TargetCampaignID,
		cats, segs, src.SubscribedDefault, nullIfEmptyStr(src.Label), now,
		src.OrganizationID, src.ID,
	)
	return err
}

func (r *leadSyncRepository) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	_, err := r.db.Exec(ctx,
		`DELETE FROM lead_sync_sources WHERE organization_id = $1 AND id = $2`, orgID, id)
	return err
}

func (r *leadSyncRepository) SetResult(ctx context.Context, id uuid.UUID, status models.LeadSyncStatus, lastSyncedAt *time.Time, resultJSON []byte, lastErr string) error {
	now := time.Now().UTC()
	_, err := r.db.Exec(ctx, `
		UPDATE lead_sync_sources SET
			status = $1, last_synced_at = $2, last_result = $3, last_error = $4, updated_at = $5
		WHERE id = $6`,
		string(status), lastSyncedAt, nullIfEmptyBytes(resultJSON), nullIfEmptyStr(lastErr), now, id)
	return err
}

// marshalJSONDefault marshals v to JSON bytes, falling back to a literal
// default (e.g. "[]") if v is nil or marshalling fails — JSONB columns are
// NOT NULL with a '[]' default, so we never write a SQL NULL there.
func marshalJSONDefault(v any, def string) []byte {
	if v == nil {
		return []byte(def)
	}
	b, err := json.Marshal(v)
	if err != nil || len(b) == 0 || string(b) == "null" {
		return []byte(def)
	}
	return b
}

func scanLeadSyncInto(row scanner, s *models.LeadSyncSource) error {
	var (
		mapping    []byte
		cats       []byte
		segs       []byte
		lastResult []byte
		status     string
		dedup      string
	)
	if err := row.Scan(
		&s.ID, &s.OrganizationID, &s.CreatedByUserID, &s.Provider, &s.ConnectionID,
		&s.SheetID, &s.SheetTitle, &s.TabTitle, &s.A1Range,
		&s.HasHeader, &mapping, &dedup, &s.TargetCampaignID, &cats, &segs,
		&s.SubscribedDefault, &s.Label, &status, &s.LastSyncedAt, &lastResult,
		&s.LastError, &s.CreatedAt, &s.UpdatedAt,
	); err != nil {
		return err
	}
	s.Status = models.LeadSyncStatus(status)
	s.Dedup = models.ContactImportDedupStrategy(dedup)

	s.ColumnMapping = []models.ContactImportColumnMapping{}
	if len(mapping) > 0 {
		_ = json.Unmarshal(mapping, &s.ColumnMapping)
	}
	s.CategoryIDs = []string{}
	if len(cats) > 0 {
		_ = json.Unmarshal(cats, &s.CategoryIDs)
	}
	s.SegmentIDs = []string{}
	if len(segs) > 0 {
		_ = json.Unmarshal(segs, &s.SegmentIDs)
	}
	if len(lastResult) > 0 {
		var res models.ContactImportResult
		if err := json.Unmarshal(lastResult, &res); err == nil {
			s.LastResult = &res
		}
	}
	return nil
}

func (r *leadSyncRepository) TargetsInOrganization(ctx context.Context, orgID uuid.UUID, campaignID *uuid.UUID, categoryIDs []string) (bool, error) {
	cats := make([]uuid.UUID, 0, len(categoryIDs))
	seen := make(map[uuid.UUID]bool, len(categoryIDs))
	for _, raw := range categoryIDs {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil {
			return false, nil
		}
		if !seen[id] {
			seen[id] = true
			cats = append(cats, id)
		}
	}
	var ok bool
	err := r.db.QueryRow(ctx, `
		SELECT ($2::uuid IS NULL OR EXISTS (SELECT 1 FROM campaigns WHERE id = $2 AND organization_id = $1))
		   AND (SELECT count(*) FROM categories WHERE id = ANY($3) AND organization_id = $1) = cardinality($3::uuid[])
	`, orgID, campaignID, cats).Scan(&ok)
	return ok, err
}
