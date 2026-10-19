package spanner

import (
	"testing"
)

func TestValidateReadOnlySQL(t *testing.T) {
	tests := []struct {
		name    string
		stmt    string
		wantErr bool
	}{
		{
			name:    "valid SELECT",
			stmt:    "SELECT * FROM Users",
			wantErr: false,
		},
		{
			name:    "valid SELECT lower",
			stmt:    "select * from Users",
			wantErr: false,
		},
		{
			name:    "valid WITH",
			stmt:    "WITH u AS (SELECT * FROM Users) SELECT * FROM u",
			wantErr: false,
		},
		{
			name:    "empty statement",
			stmt:    "",
			wantErr: true,
		},
		{
			name:    "only whitespace",
			stmt:    "   \t\n   ",
			wantErr: true,
		},
		{
			name:    "multiple statements separated by semicolon",
			stmt:    "SELECT * FROM Users; SELECT * FROM Accounts",
			wantErr: true,
		},
		{
			name:    "trailing semicolon allowed",
			stmt:    "SELECT * FROM Users;",
			wantErr: false,
		},
		{
			name:    "starts with UPDATE",
			stmt:    "UPDATE Users SET name = 'Bob' WHERE id = 1",
			wantErr: true,
		},
		{
			name:    "starts with DELETE",
			stmt:    "DELETE FROM Users WHERE id = 1",
			wantErr: true,
		},
		{
			name:    "starts with INSERT",
			stmt:    "INSERT INTO Users (id, name) VALUES (1, 'Bob')",
			wantErr: true,
		},
		{
			name:    "WITH containing UPDATE",
			stmt:    "WITH u AS (UPDATE Users SET name = 'Bob' RETURNING *) SELECT * FROM u",
			wantErr: true,
		},
		{
			name:    "SELECT with DELETE keyword",
			stmt:    "SELECT * FROM Users WHERE id = 1; DELETE FROM Users",
			wantErr: true, // caught by semicolon check, but tests the intent
		},
		{
			name: "SELECT containing write keyword but not as distinct token",
			// this should fail because `drop` is not a distinct word? No, writeKeywordRe matches \bdrop\b
			// let's test a word that *contains* drop, like `dropbox`
			stmt:    "SELECT dropbox_id FROM Users",
			wantErr: false,
		},
		{
			name:    "SELECT with write keyword as distinct token",
			stmt:    "SELECT drop FROM Users", // e.g. a column named drop, unfortunately rejected but better safe than sorry
			wantErr: true,
		},
		{
			name:    "WITH containing DELETE",
			stmt:    "WITH x AS (DELETE FROM t RETURNING *) SELECT * FROM x",
			wantErr: true,
		},
		{
			name:    "WITH containing INSERT",
			stmt:    "WITH x AS (INSERT INTO t (id) VALUES (1) RETURNING *) SELECT * FROM x",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateReadOnlySQL(tt.stmt)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateReadOnlySQL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
