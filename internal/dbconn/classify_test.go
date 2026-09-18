package dbconn

import "testing"

func TestClassifySQLReadAndWrite(t *testing.T) {
	cases := []struct {
		sql  string
		want StatementKind
	}{
		{"SELECT 1", StatementRead},
		{"  /* c */ select id from users", StatementRead},
		{"WITH x AS (SELECT 1) SELECT * FROM x", StatementRead},
		{"WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x", StatementWrite},
		{"INSERT INTO t VALUES (1)", StatementWrite},
		{"DROP TABLE users", StatementWrite},
		{"SELECT 1; DELETE FROM t", StatementUnknown},
		{"", StatementUnknown},
	}
	for _, tc := range cases {
		got := ClassifySQL(tc.sql)
		if got != tc.want {
			t.Fatalf("%q => %s, want %s", tc.sql, got, tc.want)
		}
	}
}

func TestIsLoopbackHost(t *testing.T) {
	if !IsLoopbackHost("localhost") || !IsLoopbackHost("127.0.0.1") {
		t.Fatal("loopback should be allowed")
	}
	if IsLoopbackHost("db.example.com") {
		t.Fatal("remote host must not look local")
	}
}
