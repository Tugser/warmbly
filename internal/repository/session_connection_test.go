package repository

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testSessionConnector(pool *pgxpool.Pool) SessionConnector {
	return func(ctx context.Context) (*pgx.Conn, error) {
		return pgx.ConnectConfig(ctx, pool.Config().ConnConfig)
	}
}

func TestSessionConnectorIsRequired(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func()
	}{
		{"pool link", func() { NewPoolLinkRepository(nil, nil) }},
		{"cloud link", func() { NewCloudLinkRepository(nil, nil, nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("repository accepted a missing direct session connector")
				}
			}()
			tc.open()
		})
	}
}
