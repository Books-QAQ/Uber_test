package mysql

import "testing"

func TestSplitSQLStatements(t *testing.T) {
	t.Parallel()

	sqlText := `
-- comment
CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(64) PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS orders (
    id VARCHAR(64) PRIMARY KEY
);
`

	statements := splitSQLStatements(sqlText)
	if len(statements) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(statements))
	}
	if statements[0] != "CREATE TABLE IF NOT EXISTS users (\nid VARCHAR(64) PRIMARY KEY\n);" {
		t.Fatalf("unexpected first statement: %q", statements[0])
	}
	if statements[1] != "CREATE TABLE IF NOT EXISTS orders (\nid VARCHAR(64) PRIMARY KEY\n);" {
		t.Fatalf("unexpected second statement: %q", statements[1])
	}
}
