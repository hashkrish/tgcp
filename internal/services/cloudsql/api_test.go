package cloudsql

import (
	"testing"
)

func TestValidateReadOnlySQL(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantErr bool
	}{
		// Valid read-only queries
		{"simple select", "SELECT * FROM users", false},
		{"select with limit", "SELECT id, name FROM users LIMIT 10", false},
		{"simple with", "WITH x AS (SELECT * FROM t) SELECT * FROM x", false},
		{"show tables", "SHOW TABLES", false},
		{"show databases", "SHOW DATABASES", false},
		{"show create table", "SHOW CREATE TABLE users", false},
		{"explain select", "EXPLAIN SELECT * FROM users", false},
		{"describe table", "DESCRIBE users", false},
		{"desc table", "DESC users", false},
		{"complex select", "SELECT a, b, (SELECT c FROM t2) FROM t1 JOIN t3 ON t1.id = t3.id", false},

		// Write keyword inside a string literal should be allowed
		{"write keyword in string", "SELECT 'delete' FROM users", false},
		{"write keyword in string double quote", "SELECT \"insert\" FROM users", false},
		{"write keyword in string escaped", "SELECT 'can''t update' FROM users", false},
		{"write keyword in block comment", "SELECT * FROM users /* delete */", false},
		{"write keyword in line comment", "SELECT * FROM users -- update", false},
		{"write keyword in hash comment", "SELECT * FROM users # drop", false},

		// Writes that should be blocked
		{"simple delete", "DELETE FROM users", true},
		{"simple update", "UPDATE users SET name='foo'", true},
		{"simple insert", "INSERT INTO users(id) VALUES(1)", true},
		{"drop table", "DROP TABLE users", true},
		{"create table", "CREATE TABLE new_users (id int)", true},
		{"alter table", "ALTER TABLE users DROP COLUMN name", true},
		{"truncate table", "TRUNCATE TABLE users", true},
		{"grant", "GRANT ALL ON users TO bob", true},
		{"select into outfile", "SELECT * INTO OUTFILE '/tmp/foo' FROM users", true},
		{"select into newtable", "SELECT * INTO newtable FROM users", true},

		// Hidden writes (CTEs, subqueries)
		{"write in cte", "WITH x AS (DELETE FROM users RETURNING *) SELECT * FROM x", true},
		{"write in cte 2", "WITH x AS (UPDATE users SET name='a' RETURNING *) SELECT * FROM x", true},
		{"write in cte 3", "WITH x AS (INSERT INTO users(id) VALUES(1) RETURNING *) SELECT * FROM x", true},
		{"write in subquery", "SELECT (DELETE FROM users) FROM dual", true},

		// Multiple statements
		{"multiple statements", "SELECT * FROM users; DELETE FROM users", true},
		{"multiple statements with space", "SELECT * FROM users ; DROP TABLE users", true},

		// Empty or whitespace
		{"empty string", "", true},
		{"whitespace", "   ", true},

		// Queries that start with read-only keywords but are invalid or not read-only
		{"select followed by write", "SELECT * FROM users; DELETE FROM users;", true},

		// Unescaped writes mixed with strings
		{"write after string", "SELECT 'test'; DELETE FROM users", true},
		{"write after comment", "SELECT * FROM users; /* end */ DROP TABLE users", true},

        // Token concatenation bypass
        {"block comment concat", "DELETE/**/FROM users", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateReadOnlySQL(tt.query)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateReadOnlySQL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateReadOnlySQL_Bypass(t *testing.T) {
	err := ValidateReadOnlySQL("SELECT \"safe\"; DELETE FROM users; SELECT 'safe';")
	if err == nil {
		t.Errorf("Expected error for bypass, got nil")
	}

	err = ValidateReadOnlySQL("SELECT \"safe\\\"; DELETE FROM users; SELECT 'safe';")
	if err == nil {
		t.Errorf("Expected error for bypass, got nil")
	}
}

func TestValidateReadOnlySQL_Unclosed(t *testing.T) {
	err := ValidateReadOnlySQL("SELECT \"safe")
	if err == nil || err.Error() != "unclosed string literal" {
		t.Errorf("Expected unclosed string literal error, got %v", err)
	}

	err = ValidateReadOnlySQL("SELECT /* comment")
	if err == nil || err.Error() != "unclosed block comment" {
		t.Errorf("Expected unclosed block comment error, got %v", err)
	}
}
