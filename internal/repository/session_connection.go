package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type SessionConnector func(context.Context) (*pgx.Conn, error)

func requireSessionConnector(connector SessionConnector) SessionConnector {
	if connector == nil {
		panic("direct database session connector is required")
	}
	return connector
}
