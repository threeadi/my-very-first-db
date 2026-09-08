package main

import "testing"

func TestParseSelectWhereOperators(t *testing.T) {
	currentDatabase = "testdb"

	tests := []struct {
		name    string
		opToken string
		wantOp  CompareOp
	}{
		{"eq", "=", OpEq},
		{"neq_diamond", "<>", OpNeq},
		{"neq_bang", "!=", OpNeq},
		{"gt", ">", OpGt},
		{"gte", ">=", OpGte},
		{"lt", "<", OpLt},
		{"lte", "<=", OpLte},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tokens := []Token{
				{Type: KEYWORD, Literal: "select"},
				{Type: ASTERISK, Literal: "*"},
				{Type: KEYWORD, Literal: "from"},
				{Type: IDENT, Literal: "users"},
				{Type: KEYWORD, Literal: "where"},
				{Type: IDENT, Literal: "id"},
				{Type: OPERATOR, Literal: tc.opToken},
				{Type: NUMBER, Literal: "1"},
			}

			p := NewParser(tokens)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatal(err)
			}

			sel, ok := stmt.(SelectStatement)
			if !ok {
				t.Fatalf("expected SelectStatement, got %T", stmt)
			}

			if sel.Criteria == nil {
				t.Fatal("expected Criteria terisi, got nil")
			}
			if sel.Criteria.Key != "id" || sel.Criteria.Op != tc.wantOp || sel.Criteria.Val != "1" {
				t.Fatalf("unexpected condition: %+v", sel.Criteria)
			}
			if len(sel.Criteria.Criteria) != 0 {
				t.Fatalf("expected tidak ada chain AND/OR, got: %+v", sel.Criteria.Criteria)
			}
		})
	}
}

func TestParseSelectWithoutWhere(t *testing.T) {
	currentDatabase = "testdb"

	tokens := []Token{
		{Type: KEYWORD, Literal: "select"},
		{Type: ASTERISK, Literal: "*"},
		{Type: KEYWORD, Literal: "from"},
		{Type: IDENT, Literal: "users"},
	}

	p := NewParser(tokens)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}

	sel, ok := stmt.(SelectStatement)
	if !ok {
		t.Fatalf("expected SelectStatement, got %T", stmt)
	}
	if sel.Criteria != nil {
		t.Fatalf("expected Criteria nil tanpa WHERE, got: %+v", sel.Criteria)
	}
}

// TestParseSelectWhereAndOr memverifikasi fix atas 3 bug yang tadinya ada di
// parseWhere: off-by-one saat cek token AND/OR, case "AND"/"OR" vs "and"/"or",
// dan predikat kedua yang datanya ketuker/hilang.
func TestParseSelectWhereAndOr(t *testing.T) {
	currentDatabase = "testdb"

	// where id = 1 and name = 'andi' or id = 3
	tokens := []Token{
		{Type: KEYWORD, Literal: "select"},
		{Type: ASTERISK, Literal: "*"},
		{Type: KEYWORD, Literal: "from"},
		{Type: IDENT, Literal: "users"},
		{Type: KEYWORD, Literal: "where"},
		{Type: IDENT, Literal: "id"},
		{Type: OPERATOR, Literal: "="},
		{Type: NUMBER, Literal: "1"},
		{Type: KEYWORD, Literal: "and"},
		{Type: IDENT, Literal: "name"},
		{Type: OPERATOR, Literal: "="},
		{Type: STRING, Literal: "andi"},
		{Type: KEYWORD, Literal: "or"},
		{Type: IDENT, Literal: "id"},
		{Type: OPERATOR, Literal: "="},
		{Type: NUMBER, Literal: "3"},
	}

	p := NewParser(tokens)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}

	sel, ok := stmt.(SelectStatement)
	if !ok {
		t.Fatalf("expected SelectStatement, got %T", stmt)
	}

	if sel.Criteria == nil {
		t.Fatal("expected Criteria terisi")
	}
	if sel.Criteria.Key != "id" || sel.Criteria.Op != OpEq || sel.Criteria.Val != "1" {
		t.Fatalf("predikat pertama salah: %+v", sel.Criteria)
	}
	if len(sel.Criteria.Criteria) != 2 {
		t.Fatalf("expected 2 predikat lanjutan, got %d: %+v", len(sel.Criteria.Criteria), sel.Criteria.Criteria)
	}

	second := sel.Criteria.Criteria[0]
	if second.Key != "name" || second.Op != OpEq || second.Val != "andi" || second.Logic != AND {
		t.Fatalf("predikat kedua salah: %+v", second)
	}

	third := sel.Criteria.Criteria[1]
	if third.Key != "id" || third.Op != OpEq || third.Val != "3" || third.Logic != OR {
		t.Fatalf("predikat ketiga salah: %+v", third)
	}
}

func TestParseSelectWhereMissingOperator(t *testing.T) {
	currentDatabase = "testdb"

	tokens := []Token{
		{Type: KEYWORD, Literal: "select"},
		{Type: ASTERISK, Literal: "*"},
		{Type: KEYWORD, Literal: "from"},
		{Type: IDENT, Literal: "users"},
		{Type: KEYWORD, Literal: "where"},
		{Type: IDENT, Literal: "id"},
		{Type: NUMBER, Literal: "1"}, // operator hilang
	}

	p := NewParser(tokens)
	if _, err := p.Parse(); err == nil {
		t.Fatal("expected error karena operator hilang, got nil")
	}
}

// ---------------------------------------------------------------------------
// DELETE
// ---------------------------------------------------------------------------

func TestParseDeleteWithWhere(t *testing.T) {
	currentDatabase = "testdb"

	// delete from users where id = 5
	tokens := []Token{
		{Type: KEYWORD, Literal: "delete"},
		{Type: KEYWORD, Literal: "from"},
		{Type: IDENT, Literal: "users"},
		{Type: KEYWORD, Literal: "where"},
		{Type: IDENT, Literal: "id"},
		{Type: OPERATOR, Literal: "="},
		{Type: NUMBER, Literal: "5"},
	}

	p := NewParser(tokens)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}

	del, ok := stmt.(DeleteStatement)
	if !ok {
		t.Fatalf("expected DeleteStatement, got %T", stmt)
	}

	if del.DBName != "testdb" || del.Table != "users" {
		t.Fatalf("unexpected DBName/Table: %+v", del)
	}
	if del.Criteria == nil {
		t.Fatal("expected Criteria terisi")
	}
	if del.Criteria.Key != "id" || del.Criteria.Op != OpEq || del.Criteria.Val != "5" {
		t.Fatalf("unexpected condition: %+v", del.Criteria)
	}
}

func TestParseDeleteWithoutWhere(t *testing.T) {
	currentDatabase = "testdb"

	// delete from users -- menghapus seluruh row, sama seperti SELECT *
	// tanpa WHERE.
	tokens := []Token{
		{Type: KEYWORD, Literal: "delete"},
		{Type: KEYWORD, Literal: "from"},
		{Type: IDENT, Literal: "users"},
	}

	p := NewParser(tokens)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}

	del, ok := stmt.(DeleteStatement)
	if !ok {
		t.Fatalf("expected DeleteStatement, got %T", stmt)
	}
	if del.Criteria != nil {
		t.Fatalf("expected Criteria nil tanpa WHERE, got: %+v", del.Criteria)
	}
}

func TestParseDeleteMissingFrom(t *testing.T) {
	currentDatabase = "testdb"

	tokens := []Token{
		{Type: KEYWORD, Literal: "delete"},
		{Type: IDENT, Literal: "users"},
	}

	p := NewParser(tokens)
	if _, err := p.Parse(); err == nil {
		t.Fatal("expected error karena FROM hilang, got nil")
	}
}

func TestParseDeleteMissingTable(t *testing.T) {
	currentDatabase = "testdb"

	tokens := []Token{
		{Type: KEYWORD, Literal: "delete"},
		{Type: KEYWORD, Literal: "from"},
	}

	p := NewParser(tokens)
	if _, err := p.Parse(); err == nil {
		t.Fatal("expected error karena nama table hilang, got nil")
	}
}

func TestParseDeleteMissingWhereKeywordRejectsTrailingTokens(t *testing.T) {
	currentDatabase = "testdb"

	tokens := []Token{
		{Type: KEYWORD, Literal: "delete"},
		{Type: KEYWORD, Literal: "from"},
		{Type: IDENT, Literal: "users"},
		// "where" hilang -- langsung lanjut predikat
		{Type: IDENT, Literal: "id"},
		{Type: OPERATOR, Literal: "="},
		{Type: NUMBER, Literal: "1"},
	}

	p := NewParser(tokens)
	stmt, err := p.Parse()
	if err == nil {
		t.Fatalf("expected error karena trailing token tanpa WHERE, got statement: %+v", stmt)
	}
}

func TestParseSelectRejectsTrailingTokens(t *testing.T) {
	currentDatabase = "testdb"

	// select * from users garbage
	tokens := []Token{
		{Type: KEYWORD, Literal: "select"},
		{Type: ASTERISK, Literal: "*"},
		{Type: KEYWORD, Literal: "from"},
		{Type: IDENT, Literal: "users"},
		{Type: IDENT, Literal: "garbage"},
	}

	p := NewParser(tokens)
	if _, err := p.Parse(); err == nil {
		t.Fatal("expected error karena trailing token setelah SELECT, got nil")
	}
}

func TestParseAcceptsTrailingSemicolon(t *testing.T) {
	currentDatabase = "testdb"

	tokens := []Token{
		{Type: KEYWORD, Literal: "delete"},
		{Type: KEYWORD, Literal: "from"},
		{Type: IDENT, Literal: "users"},
		{Type: KEYWORD, Literal: "where"},
		{Type: IDENT, Literal: "id"},
		{Type: OPERATOR, Literal: "="},
		{Type: NUMBER, Literal: "1"},
		{Type: DELIMITER, Literal: ";"},
	}

	p := NewParser(tokens)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("expected ';' di akhir diterima, got error: %v", err)
	}

	del, ok := stmt.(DeleteStatement)
	if !ok {
		t.Fatalf("expected DeleteStatement, got %T", stmt)
	}
	if del.Criteria == nil || del.Criteria.Key != "id" {
		t.Fatalf("unexpected criteria: %+v", del.Criteria)
	}
}

// TestParseRejectsTokensAfterSemicolon menegaskan ";" cuma melangkahi
// DELIMITER, bukan "matikan validasi trailing-token seterusnya" -- kalau
// masih ada token NON-DELIMITER setelah ";", itu tetap harus error.
func TestParseRejectsTokensAfterSemicolon(t *testing.T) {
	currentDatabase = "testdb"

	tokens := []Token{
		{Type: KEYWORD, Literal: "delete"},
		{Type: KEYWORD, Literal: "from"},
		{Type: IDENT, Literal: "users"},
		{Type: DELIMITER, Literal: ";"},
		{Type: IDENT, Literal: "garbage"},
	}

	p := NewParser(tokens)
	if _, err := p.Parse(); err == nil {
		t.Fatal("expected error karena ada token setelah ';', got nil")
	}
}

func TestParseSelectWhereMissingValueAfterAnd(t *testing.T) {
	currentDatabase = "testdb"

	// where id = 1 and  <- predikat kedua tidak lengkap
	tokens := []Token{
		{Type: KEYWORD, Literal: "select"},
		{Type: ASTERISK, Literal: "*"},
		{Type: KEYWORD, Literal: "from"},
		{Type: IDENT, Literal: "users"},
		{Type: KEYWORD, Literal: "where"},
		{Type: IDENT, Literal: "id"},
		{Type: OPERATOR, Literal: "="},
		{Type: NUMBER, Literal: "1"},
		{Type: KEYWORD, Literal: "and"},
	}

	p := NewParser(tokens)
	if _, err := p.Parse(); err == nil {
		t.Fatal("expected error karena predikat setelah AND tidak lengkap, got nil")
	}
}
