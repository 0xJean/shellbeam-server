package jsonrpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/pkg/metric"
	"github.com/komari-monitor/komari/pkg/rpc"
)

func dbQuery(sql string, args ...any) (any, *rpc.JsonRpcError) {
	return adminDBQuery(context.Background(), &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Method: "admin:dbQuery",
		Params: map[string]any{"sql": sql, "args": args},
	})
}

func TestDBQueryIsReadOnly(t *testing.T) {
	db := dbcore.GetDBInstance()
	if err := db.Exec("CREATE TABLE IF NOT EXISTS dbquery_probe (value TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM dbquery_probe").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO dbquery_probe VALUES ('kept')").Error; err != nil {
		t.Fatal(err)
	}
	count := func() int64 {
		var n int64
		if err := db.Raw("SELECT COUNT(*) FROM dbquery_probe").Scan(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	target := filepath.Join(t.TempDir(), "copy.db")
	attached := filepath.Join(t.TempDir(), "attached.db")

	for _, statement := range []string{
		"INSERT INTO dbquery_probe VALUES ('x')",
		"DELETE FROM dbquery_probe",
		"UPDATE dbquery_probe SET value = 'x'",
		"DROP TABLE dbquery_probe",
		"CREATE TABLE dbquery_other (a)",
		"VACUUM INTO '" + target + "'",
		"VACUUM",
		"ATTACH DATABASE '" + attached + "' AS other",
		"PRAGMA query_only = OFF",
		"PRAGMA writable_schema = ON",
		"SELECT 1; DELETE FROM dbquery_probe",
		"PRAGMA table_info(dbquery_probe); DELETE FROM dbquery_probe",
		"SELECT 1 /* ; */; DELETE FROM dbquery_probe -- ",
		"WITH gone AS (SELECT 1) DELETE FROM dbquery_probe",
		"WITH gone AS (SELECT 1) INSERT INTO dbquery_probe VALUES ('x')",
	} {
		if _, err := dbQuery(statement); err == nil {
			t.Errorf("%q succeeded", statement)
		}
	}
	if got := count(); got != 1 {
		t.Fatalf("rows after writes = %d, want 1", got)
	}
	for _, path := range []string{target, attached} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s was created: %v", path, err)
		}
	}

	// Reads still work, and the pooled connection is writable afterwards.
	result, err := dbQuery("SELECT value FROM dbquery_probe WHERE value = ?", "kept")
	if err != nil {
		t.Fatalf("select: %+v", err)
	}
	if rows := result.(databaseQueryResponse).RowCount; rows != 1 {
		t.Fatalf("select rows = %d", rows)
	}
	for _, statement := range []string{
		"  select ';' , \"value\" from dbquery_probe; -- done\n",
		"WITH kept AS (SELECT value FROM dbquery_probe) SELECT * FROM kept",
		"EXPLAIN QUERY PLAN SELECT * FROM dbquery_probe",
		"PRAGMA table_info(dbquery_probe)",
		"VALUES (1)",
	} {
		if _, err := dbQuery(statement); err != nil {
			t.Errorf("%q: %+v", statement, err)
		}
	}
	if err := db.Exec("INSERT INTO dbquery_probe VALUES ('after')").Error; err != nil {
		t.Fatalf("server write after a query: %v", err)
	}
	var queryOnly int
	if err := db.Raw("PRAGMA query_only").Scan(&queryOnly).Error; err != nil || queryOnly != 0 {
		t.Fatalf("query_only left at %d: %v", queryOnly, err)
	}
}

// The connection refuses writes even if a statement gets past the keyword check.
// query_only does not stop VACUUM INTO; only validateReadOnlySQL does.
func TestSQLiteReadOnlyConnectionRefusesWrites(t *testing.T) {
	db := dbcore.GetDBInstance()
	if err := db.Exec("CREATE TABLE IF NOT EXISTS dbquery_conn_probe (value TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	rows, release, err := metric.QueryReadOnly(context.Background(), sqlDB, metric.DriverSQLite, "INSERT INTO dbquery_conn_probe VALUES ('x')")
	if err == nil {
		failed := !rows.Next() && rows.Err() != nil
		_ = rows.Close()
		release()
		if !failed {
			t.Fatal("insert ran on a read-only connection")
		}
	}
	var n int64
	if err := db.Raw("SELECT COUNT(*) FROM dbquery_conn_probe").Scan(&n).Error; err != nil || n != 0 {
		t.Fatalf("rows = %d: %v", n, err)
	}
}

func TestValidateReadOnlySQLByDriver(t *testing.T) {
	cases := []struct {
		driver    metric.Driver
		statement string
		ok        bool
	}{
		{metric.DriverMySQL, "SELECT * FROM t INTO OUTFILE '/tmp/x'", false},
		{metric.DriverMySQL, "SELECT /*! 1; DROP TABLE t */", false},
		{metric.DriverMySQL, `SELECT '\''; DROP TABLE t; -- '`, false},
		{metric.DriverMySQL, "SHOW TABLES", true},
		{metric.DriverMySQL, "SELECT 5--1", true},
		{metric.DriverPostgreSQL, "SELECT * INTO copy FROM t", false},
		{metric.DriverPostgreSQL, "COPY t TO '/tmp/x'", false},
		{metric.DriverPostgreSQL, "SELECT a[1] FROM t", true},
		{metric.DriverPostgreSQL, "SHOW server_version", true},
		{metric.DriverSQLite, "SHOW TABLES", false},
		{metric.DriverSQLite, "SELECT 'unterminated", false},
		{metric.DriverSQLite, "", false},
		{metric.DriverSQLite, "-- only a comment", false},
		{metric.DriverSQLite, `SELECT 'into', "into" FROM t`, true},
	}
	for _, tc := range cases {
		if err := validateReadOnlySQL(tc.driver, tc.statement); (err == nil) != tc.ok {
			t.Errorf("%s %q: err = %v, want ok = %v", tc.driver, tc.statement, err, tc.ok)
		}
	}
}
