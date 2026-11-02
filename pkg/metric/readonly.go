package metric

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"
)

// QueryReadOnly runs a query with writes refused by the database itself: SQLite
// through PRAGMA query_only on a dedicated connection, MySQL and PostgreSQL through
// a read-only transaction. The caller must close rows, then call release.
//
// The statement must be a single one, or a SQLite script could switch the pragma
// back off and a MySQL or PostgreSQL one could end the transaction.
func QueryReadOnly(ctx context.Context, db *sql.DB, backend Driver, query string, args ...any) (*sql.Rows, func(), error) {
	if backend == DriverSQLite {
		return querySQLiteReadOnly(ctx, db, query, args...)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	return rows, func() { _ = tx.Rollback() }, nil
}

func querySQLiteReadOnly(ctx context.Context, db *sql.DB, query string, args ...any) (*sql.Rows, func(), error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	var previous int
	if err := conn.QueryRowContext(ctx, "PRAGMA query_only").Scan(&previous); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	// Connections return to a pool the server writes through, so a connection that
	// cannot be restored is dropped instead of reused.
	release := func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(restoreCtx, fmt.Sprintf("PRAGMA query_only = %d", previous)); err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
		release()
		return nil, nil, err
	}
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		release()
		return nil, nil, err
	}
	return rows, release, nil
}
