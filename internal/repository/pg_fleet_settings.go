package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
)

// FleetSettingsRepository holds the two instance-wide values the pull-based
// fleet needs: what version nodes should run, and the hash of the token they
// join with. Both live in admin_settings, so they are shared across backend
// replicas and survive a restart.
type FleetSettingsRepository interface {
	GetRelease(ctx context.Context) (*models.FleetReleaseState, error)
	SetRelease(ctx context.Context, state *models.FleetReleaseState) error
	// CompareAndSetRelease writes next only while the stored state still
	// equals prev (nil meaning none stored), so concurrent backends never
	// overwrite each other's rollout decisions. It reports whether it wrote.
	CompareAndSetRelease(ctx context.Context, prev, next *models.FleetReleaseState) (bool, error)

	// GetJoinToken returns "" when no token has been issued yet, which callers
	// must treat as "nothing may join" rather than "anything may". A nil
	// expiry is a token issued before tokens carried one.
	GetJoinToken(ctx context.Context) (hash string, expiresAt *time.Time, err error)
	SetJoinToken(ctx context.Context, hash string, expiresAt time.Time) error
}

type fleetSettingsRepository struct {
	db *db.DB
}

func NewFleetSettingsRepository(d *db.DB) FleetSettingsRepository {
	return &fleetSettingsRepository{db: d}
}

func (r *fleetSettingsRepository) getRaw(ctx context.Context, key string) ([]byte, error) {
	var raw []byte
	err := r.db.QueryRow(ctx, `SELECT value FROM admin_settings WHERE key = $1`, key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return raw, err
}

func (r *fleetSettingsRepository) setRaw(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO admin_settings (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, key, raw)
	return err
}

func (r *fleetSettingsRepository) GetRelease(ctx context.Context) (*models.FleetReleaseState, error) {
	raw, err := r.getRaw(ctx, models.FleetSettingsKeyRelease)
	if err != nil || raw == nil {
		return nil, err
	}
	var st models.FleetReleaseState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (r *fleetSettingsRepository) SetRelease(ctx context.Context, state *models.FleetReleaseState) error {
	return r.setRaw(ctx, models.FleetSettingsKeyRelease, state)
}

func (r *fleetSettingsRepository) CompareAndSetRelease(ctx context.Context, prev, next *models.FleetReleaseState) (bool, error) {
	raw, err := json.Marshal(next)
	if err != nil {
		return false, err
	}
	if prev == nil {
		tag, err := r.db.Exec(ctx, `
			INSERT INTO admin_settings (key, value, updated_at) VALUES ($1, $2, now())
			ON CONFLICT (key) DO NOTHING`, models.FleetSettingsKeyRelease, raw)
		return err == nil && tag.RowsAffected() == 1, err
	}
	old, err := json.Marshal(prev)
	if err != nil {
		return false, err
	}
	// jsonb equality is semantic, so key order in the stored value is irrelevant.
	tag, err := r.db.Exec(ctx, `
		UPDATE admin_settings SET value = $2, updated_at = now()
		 WHERE key = $1 AND value = $3::jsonb`, models.FleetSettingsKeyRelease, raw, old)
	return err == nil && tag.RowsAffected() == 1, err
}

type joinTokenValue struct {
	Hash      string     `json:"hash"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func (r *fleetSettingsRepository) GetJoinToken(ctx context.Context) (string, *time.Time, error) {
	raw, err := r.getRaw(ctx, models.FleetSettingsKeyJoinToken)
	if err != nil || raw == nil {
		return "", nil, err
	}
	var v joinTokenValue
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", nil, err
	}
	return v.Hash, v.ExpiresAt, nil
}

func (r *fleetSettingsRepository) SetJoinToken(ctx context.Context, hash string, expiresAt time.Time) error {
	return r.setRaw(ctx, models.FleetSettingsKeyJoinToken, joinTokenValue{Hash: hash, ExpiresAt: &expiresAt})
}
