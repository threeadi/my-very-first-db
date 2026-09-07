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
			if len(sel.Criteria.Condition) != 0 {
				t.Fatalf("expected tidak ada chain AND/OR, got: %+v", sel.Criteria.Condition)
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
	if len(sel.Criteria.Condition) != 2 {
		t.Fatalf("expected 2 predikat lanjutan, got %d: %+v", len(sel.Criteria.Condition), sel.Criteria.Condition)
	}

	second := sel.Criteria.Condition[0]
	if second.Key != "name" || second.Op != OpEq || second.Val != "andi" || second.Logic != AND {
		t.Fatalf("predikat kedua salah: %+v", second)
	}

	third := sel.Criteria.Condition[1]
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
