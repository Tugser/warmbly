package db

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func transactionPooling() (bool, error) {
	switch strings.TrimSpace(os.Getenv("DB_POOL_MODE")) {
	case "", "direct":
		return false, nil
	case "transaction":
		return true, nil
	default:
		return false, errors.New("DB_POOL_MODE must be direct or transaction")
	}
}

// DirectEndpoint resolves the connection used by migrations and session locks.
func DirectEndpoint(endpoint, directEndpoint string) (string, error) {
	pooled, err := transactionPooling()
	if err != nil {
		return "", err
	}
	if directEndpoint != "" {
		if pooled && directEndpoint == endpoint {
			return "", errors.New("transaction pooling requires a distinct PRIMARY_DB_DIRECT connection")
		}
		primary, err := pgxpool.ParseConfig(endpoint)
		if err != nil {
			return "", errors.New("invalid PRIMARY_DB connection configuration")
		}
		direct, err := pgxpool.ParseConfig(directEndpoint)
		if err != nil {
			return "", errors.New("invalid PRIMARY_DB_DIRECT connection configuration")
		}
		if primary.ConnConfig.Database != direct.ConnConfig.Database {
			return "", errors.New("PRIMARY_DB and PRIMARY_DB_DIRECT must select the same database")
		}
		return directEndpoint, nil
	}
	if pooled {
		return "", errors.New("PRIMARY_DB_DIRECT is required with transaction pooling")
	}
	return endpoint, nil
}

func connectionConfigs(endpoint, directEndpoint string) (*pgxpool.Config, *pgx.ConnConfig, bool, error) {
	pooled, err := transactionPooling()
	if err != nil {
		return nil, nil, false, err
	}
	directEndpoint, err = DirectEndpoint(endpoint, directEndpoint)
	if err != nil {
		return nil, nil, false, err
	}
	pool, err := pgxpool.ParseConfig(endpoint)
	if err != nil {
		return nil, nil, false, errors.New("invalid PRIMARY_DB connection configuration")
	}
	directPool, err := pgxpool.ParseConfig(directEndpoint)
	if err != nil {
		return nil, nil, false, errors.New("invalid PRIMARY_DB_DIRECT connection configuration")
	}
	direct := directPool.ConnConfig
	direct.ConnectTimeout = defaultConnectTimeout
	direct.RuntimeParams["idle_in_transaction_session_timeout"] = idleInTxnTimeoutMs
	direct.RuntimeParams["statement_timeout"] = statementTimeoutMs
	if pooled {
		delete(pool.ConnConfig.RuntimeParams, "idle_in_transaction_session_timeout")
		delete(pool.ConnConfig.RuntimeParams, "statement_timeout")
		pool.AfterConnect = verifyPooledTimeouts
	} else {
		pool.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = idleInTxnTimeoutMs
		pool.ConnConfig.RuntimeParams["statement_timeout"] = statementTimeoutMs
	}
	return pool, direct, pooled, nil
}

// Transaction pooling relies on role defaults, which must apply to every server connection.
func verifyPooledTimeouts(ctx context.Context, conn *pgx.Conn) error {
	var statement, idle int64
	err := conn.QueryRow(ctx, `SELECT
		(SELECT setting::bigint FROM pg_settings WHERE name='statement_timeout'),
		(SELECT setting::bigint FROM pg_settings WHERE name='idle_in_transaction_session_timeout')`).Scan(&statement, &idle)
	if err != nil {
		return errors.New("cannot verify transaction-pool database timeouts")
	}
	if statement <= 0 || statement > 60000 || idle <= 0 || idle > 300000 {
		return errors.New("transaction pooling requires role defaults: statement_timeout at most 60s and idle_in_transaction_session_timeout at most 5min, both nonzero")
	}
	return nil
}
