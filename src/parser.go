package main

import (
	"errors"
	"fmt"
	"strings"
)

type TokenType int

const (
	KEYWORD TokenType = iota
	IDENT
	NUMBER
	STRING
	COMMA
	ASTERISK
	OPERATOR
	LPAREN
	RPAREN
	DELIMITER
	EOF
)

type Token struct {
	Type    TokenType
	Literal string
}

type Parser struct {
	Tokens []Token
	pos    int
}

// Statement adalah tipe umum untuk seluruh node statement SQL.
// Marker method ini mencegah tipe sembarang dianggap sebagai Statement.
type Statement interface {
	statementNode()
}

type CreateDatabaseStatement struct {
	DBName string
}

func (CreateDatabaseStatement) statementNode() {}

type DropDatabaseStatement struct {
	DBName string
}

func (DropDatabaseStatement) statementNode() {}

type DropTableStatement struct {
	DBName string
	Table  string
}

func (DropTableStatement) statementNode() {}

type CreateTableStatement struct {
	DBName  string
	Table   string
	Columns []ColumnDef
}

func (CreateTableStatement) statementNode() {}

type SelectStatement struct {
	DBName   string
	Table    string
	Columns  []string
	Criteria *WhereClause
	Sort     Sort
}

type CompareOp int

const (
	OpEq  CompareOp = iota // =
	OpNeq                  // <>
	OpGt                   // >
	OpGte                  // >=
	OpLt                   // <
	OpLte                  // <=
)

type LogicOp int

const (
	AND LogicOp = iota
	OR
)

type WhereClause struct {
	Key      string
	Op       CompareOp
	Val      any
	Logic    LogicOp
	Criteria []*WhereClause
}

type Sort struct {
	Key string
	Dir string
}

func (SelectStatement) statementNode() {}

type InsertStatement struct {
	DBName  string
	Table   string
	Columns []string
	Values  []string
}

func (InsertStatement) statementNode() {}

type DeleteStatement struct {
	DBName   string
	Table    string
	Criteria *WhereClause
}

func (DeleteStatement) statementNode() {}

func NewParser(tokens []Token) Parser {
	return Parser{
		Tokens: tokens,
		pos:    0,
	}
}

func (p *Parser) Parse() (Statement, error) {
	if len(p.Tokens) == 0 {
		return nil, ErrUnexpectedEOF
	}
	if p.Tokens[0].Type != KEYWORD {
		return nil, ErrInvalidStatement
	}

	var stmt Statement
	var err error

	switch p.Tokens[0].Literal {
	case "drop":
		if len(p.Tokens) < 2 {
			return nil, ErrUnexpectedEOF
		}

		switch p.Tokens[1].Literal {
		case "database":
			stmt, err = p.parseDropDB()
		case "table":
			stmt, err = p.parseDropTable()
		default:
			return nil, fmt.Errorf("%w: DROP %s", ErrInvalidStatement, p.Tokens[1].Literal)
		}

	case "create":
		if len(p.Tokens) < 2 {
			return nil, ErrUnexpectedEOF
		}

		switch p.Tokens[1].Literal {
		case "database":
			stmt, err = p.parseCreateDB()
		case "table":
			stmt, err = p.parseCreateTable()
		default:
			return nil, fmt.Errorf("%w: CREATE %s", ErrInvalidStatement, p.Tokens[1].Literal)
		}

	case "select":
		stmt, err = p.parseSelect()

	case "insert":
		stmt, err = p.parseInsert()

	case "delete":
		stmt, err = p.parseDelete()

	default:
		return nil, fmt.Errorf("%w: %s", ErrInvalidStatement, p.Tokens[0].Literal)
	}

	if err != nil {
		return nil, err
	}

	for p.pos < len(p.Tokens) && p.Tokens[p.pos].Type == DELIMITER {
		p.pos++
	}

	if p.pos < len(p.Tokens) {
		return nil, fmt.Errorf(
			"%w: token tidak terduga setelah statement: %s",
			ErrInvalidStatement,
			p.Tokens[p.pos].Literal,
		)
	}

	return stmt, nil
}

func (p *Parser) parseCreateDB() (Statement, error) {
	p.pos += 2
	if p.pos >= len(p.Tokens) {
		return nil, fmt.Errorf("%w: diharapkan nama database", ErrUnexpectedEOF)
	}

	var dbName strings.Builder
	for p.pos < len(p.Tokens) {
		token := p.Tokens[p.pos]
		if token.Type != IDENT && token.Type != STRING && token.Type != NUMBER {
			return nil, fmt.Errorf("%w: nama database tidak valid: %s", ErrInvalidStatement, token.Literal)
		}

		if _, err := dbName.WriteString(token.Literal); err != nil {
			return nil, errors.Join(ErrInvalidStatement, err)
		}
		p.pos++
	}

	return CreateDatabaseStatement{DBName: dbName.String()}, nil
}

func (p *Parser) parseCreateTable() (Statement, error) {
	p.pos += 2
	if p.pos >= len(p.Tokens) {
		return nil, fmt.Errorf("%w: diharapkan nama table", ErrUnexpectedEOF)
	}
	if p.Tokens[p.pos].Type != IDENT {
		return nil, fmt.Errorf("%w: diharapkan nama table, tapi dapat: %s", ErrInvalidStatement, p.Tokens[p.pos].Literal)
	}

	table := p.Tokens[p.pos].Literal
	p.pos++
	if p.pos >= len(p.Tokens) {
		return nil, fmt.Errorf("%w: diharapkan '('", ErrUnexpectedEOF)
	}
	if p.Tokens[p.pos].Type != LPAREN {
		return nil, fmt.Errorf("%w: diharapkan '(', tapi dapat: %s", ErrInvalidStatement, p.Tokens[p.pos].Literal)
	}

	p.pos++
	columns, err := p.parseColumns()
	if err != nil {
		return nil, errors.Join(ErrInvalidStatement, err)
	}

	return CreateTableStatement{
		DBName:  currentDatabase,
		Table:   table,
		Columns: columns,
	}, nil
}

func (p *Parser) parseColumns() ([]ColumnDef, error) {
	var columns []ColumnDef

	for p.pos < len(p.Tokens) {
		if p.Tokens[p.pos].Type != IDENT {
			return columns, fmt.Errorf("diharapkan nama kolom, tapi dapat: %s", p.Tokens[p.pos].Literal)
		}
		colName := p.Tokens[p.pos].Literal
		p.pos++

		if p.pos >= len(p.Tokens) {
			return columns, fmt.Errorf("%w: diharapkan tipe kolom", ErrUnexpectedEOF)
		}
		if p.Tokens[p.pos].Type != KEYWORD {
			return columns, fmt.Errorf("diharapkan tipe kolom, tapi dapat: %s", p.Tokens[p.pos].Literal)
		}
		colType := p.Tokens[p.pos].Literal
		p.pos++
		pk := false
		if p.pos < len(p.Tokens) && p.Tokens[p.pos].Type == KEYWORD && p.Tokens[p.pos].Literal == "primary" {
			p.pos++
			if p.pos < len(p.Tokens) && p.Tokens[p.pos].Type == KEYWORD && p.Tokens[p.pos].Literal == "key" {
				pk = true
				p.pos++
			} else {
				found := "EOF"
				if p.pos < len(p.Tokens) {
					found = p.Tokens[p.pos].Literal
				}
				return columns, fmt.Errorf("diharapkan 'key' setelah 'primary', tapi dapat: %s", found)
			}
		}

		nullable := true
		if pk {
			nullable = false
		}

		if p.pos < len(p.Tokens) && p.Tokens[p.pos].Type == KEYWORD && p.Tokens[p.pos].Literal == "not" {
			p.pos++
			if p.pos < len(p.Tokens) && p.Tokens[p.pos].Type == KEYWORD && p.Tokens[p.pos].Literal == "null" {
				nullable = false
				p.pos++
			} else {
				found := "EOF"
				if p.pos < len(p.Tokens) {
					found = p.Tokens[p.pos].Literal
				}
				return columns, fmt.Errorf("diharapkan 'null' setelah 'not', tapi dapat: %s", found)
			}
		}

		var defaultValue any
		if p.pos < len(p.Tokens) && p.Tokens[p.pos].Type == KEYWORD && p.Tokens[p.pos].Literal == "default" {
			p.pos++
			if p.pos < len(p.Tokens) && (p.Tokens[p.pos].Type == STRING || p.Tokens[p.pos].Type == NUMBER) {
				defaultValue = p.Tokens[p.pos].Literal
				p.pos++
			} else {
				found := "EOF"
				if p.pos < len(p.Tokens) {
					found = p.Tokens[p.pos].Literal
				}
				return columns, fmt.Errorf("diharapkan nilai default setelah 'default', tapi dapat: %s", found)
			}
		}

		valueType := ValueType(colType)
		if valueType != VarcharType && valueType != IntType && valueType != BooleanType && valueType != FloatType {
			return columns, fmt.Errorf("tipe kolom '%s' tidak valid", colType)
		}

		if pk && nullable {
			return columns, fmt.Errorf("PRIMARY KEY tidak boleh NULLABLE")
		}

		if pk && valueType != IntType {
			return columns, fmt.Errorf("PRIMARY KEY hanya boleh bertipe integer")
		}

		columns = append(columns, ColumnDef{
			Name:         colName,
			ValueType:    valueType,
			Nullable:     nullable,
			DefaultValue: defaultValue,
			Primary:      pk,
		})

		if p.pos >= len(p.Tokens) {
			return columns, fmt.Errorf("%w: diharapkan ',' atau ')'", ErrUnexpectedEOF)
		}

		switch p.Tokens[p.pos].Type {
		case COMMA:
			p.pos++
		case RPAREN:
			p.pos++
			return columns, nil
		default:
			return columns, fmt.Errorf("diharapkan ',' atau ')', tapi dapat: %s", p.Tokens[p.pos].Literal)
		}
	}

	return columns, fmt.Errorf("%w: diharapkan ')'", ErrUnexpectedEOF)
}

func (p *Parser) parseSelect() (Statement, error) {
	p.pos++
	if p.pos >= len(p.Tokens) {
		return nil, fmt.Errorf("%w: diharapkan nama kolom atau *", ErrUnexpectedEOF)
	}

	var columns []string
	if p.Tokens[p.pos].Type != ASTERISK && p.Tokens[p.pos].Type != IDENT {
		return nil, fmt.Errorf("%w: diharapkan nama kolom atau *, tapi dapet: %s", ErrInvalidStatement, p.Tokens[p.pos].Literal)
	}

	for p.pos < len(p.Tokens) {
		token := p.Tokens[p.pos]
		if token.Type == ASTERISK {
			columns = append(columns, "*")
			p.pos++
			break
		}
		if token.Type != IDENT {
			return nil, fmt.Errorf("%w: diharapkan nama kolom, tapi dapat %s", ErrInvalidStatement, token.Literal)
		}

		columns = append(columns, token.Literal)
		p.pos++
		if p.pos >= len(p.Tokens) {
			return nil, fmt.Errorf("%w: diharapkan FROM", ErrUnexpectedEOF)
		}

		if p.Tokens[p.pos].Type == COMMA {
			p.pos++
			continue
		}
		break
	}

	if p.pos >= len(p.Tokens) {
		return nil, fmt.Errorf("%w: diharapkan FROM", ErrUnexpectedEOF)
	}
	if p.Tokens[p.pos].Type != KEYWORD || p.Tokens[p.pos].Literal != "from" {
		return nil, fmt.Errorf("%w: diharapkan FROM, tapi dapet: %s", ErrInvalidStatement, p.Tokens[p.pos].Literal)
	}

	p.pos++
	if p.pos >= len(p.Tokens) {
		return nil, fmt.Errorf("%w: diharapkan nama table", ErrUnexpectedEOF)
	}
	if p.Tokens[p.pos].Type != IDENT {
		return nil, fmt.Errorf("%w: diharapkan nama table, tapi dapet: %s", ErrInvalidStatement, p.Tokens[p.pos].Literal)
	}

	table := p.Tokens[p.pos].Literal
	p.pos++

	stmt := SelectStatement{
		DBName:  currentDatabase,
		Table:   table,
		Columns: columns,
	}

	if p.pos < len(p.Tokens) && p.Tokens[p.pos].Type == KEYWORD && p.Tokens[p.pos].Literal == "where" {
		p.pos++
		criteria, err := p.parseWhere()
		if err != nil {
			return nil, err
		}
		stmt.Criteria = criteria
	}

	return stmt, nil
}

func (p *Parser) parseWhere() (*WhereClause, error) {
	criteria, err := p.parseComparison()
	if err != nil {
		return nil, err
	}

	for p.pos < len(p.Tokens) &&
		p.Tokens[p.pos].Type == KEYWORD &&
		(p.Tokens[p.pos].Literal == "and" || p.Tokens[p.pos].Literal == "or") {

		var logic LogicOp
		switch p.Tokens[p.pos].Literal {
		case "and":
			logic = AND
		case "or":
			logic = OR
		}
		p.pos++ // lewati AND/OR

		next, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		next.Logic = logic

		criteria.Criteria = append(criteria.Criteria, next)
	}

	return criteria, nil
}

// parseComparison "<kolom> <operator> <nilai>"
func (p *Parser) parseComparison() (*WhereClause, error) {
	if p.pos >= len(p.Tokens) {
		return nil, fmt.Errorf("%w: diharapkan nama kolom setelah WHERE", ErrUnexpectedEOF)
	}
	if p.Tokens[p.pos].Type != IDENT {
		return nil, fmt.Errorf("%w: diharapkan nama kolom setelah WHERE, tapi dapat: %s", ErrInvalidStatement, p.Tokens[p.pos].Literal)
	}

	key := p.Tokens[p.pos].Literal
	p.pos++

	if p.pos >= len(p.Tokens) {
		return nil, fmt.Errorf("%w: diharapkan operator perbandingan", ErrUnexpectedEOF)
	}
	if p.Tokens[p.pos].Type != OPERATOR {
		return nil, fmt.Errorf("%w: diharapkan operator (=, <>, >, >=, <, <=), tapi dapat: %s", ErrInvalidStatement, p.Tokens[p.pos].Literal)
	}

	var op CompareOp
	switch p.Tokens[p.pos].Literal {
	case "=":
		op = OpEq
	case "<>", "!=":
		op = OpNeq
	case ">":
		op = OpGt
	case ">=":
		op = OpGte
	case "<":
		op = OpLt
	case "<=":
		op = OpLte
	default:
		return nil, fmt.Errorf("%w: operator tidak dikenal: %s", ErrInvalidStatement, p.Tokens[p.pos].Literal)
	}
	p.pos++

	if p.pos >= len(p.Tokens) {
		return nil, fmt.Errorf("%w: diharapkan nilai setelah operator", ErrUnexpectedEOF)
	}

	valueToken := p.Tokens[p.pos]
	isNull := valueToken.Type == KEYWORD && valueToken.Literal == "null"
	if valueToken.Type != NUMBER && valueToken.Type != STRING && !isNull {
		return nil, fmt.Errorf("%w: nilai WHERE harus berupa angka, string, atau NULL", ErrInvalidStatement)
	}
	p.pos++

	return &WhereClause{
		Key: key,
		Op:  op,
		Val: valueToken.Literal,
	}, nil
}

func (p *Parser) parseInsert() (Statement, error) {
	p.pos++ // lewati INSERT
	if p.pos >= len(p.Tokens) {
		return nil, ErrUnexpectedEOF
	}
	if p.Tokens[p.pos].Type != KEYWORD || p.Tokens[p.pos].Literal != "into" {
		return nil, fmt.Errorf("%w: diharapkan INTO", ErrInvalidStatement)
	}

	p.pos++
	if p.pos >= len(p.Tokens) {
		return nil, fmt.Errorf("%w: diharapkan nama table", ErrUnexpectedEOF)
	}
	if p.Tokens[p.pos].Type != IDENT {
		return nil, fmt.Errorf("%w: diharapkan nama table", ErrInvalidStatement)
	}

	table := p.Tokens[p.pos].Literal
	p.pos++

	var columns []string
	if p.pos < len(p.Tokens) && p.Tokens[p.pos].Type == LPAREN {
		p.pos++
		for {
			if p.pos >= len(p.Tokens) {
				return nil, fmt.Errorf("%w: diharapkan nama kolom atau ')'", ErrUnexpectedEOF)
			}
			if p.Tokens[p.pos].Type != IDENT {
				return nil, fmt.Errorf("%w: diharapkan nama kolom", ErrInvalidStatement)
			}

			columns = append(columns, p.Tokens[p.pos].Literal)
			p.pos++
			if p.pos >= len(p.Tokens) {
				return nil, fmt.Errorf("%w: diharapkan ',' atau ')'", ErrUnexpectedEOF)
			}

			if p.Tokens[p.pos].Type == COMMA {
				p.pos++
				continue
			}
			if p.Tokens[p.pos].Type == RPAREN {
				p.pos++
				break
			}
			return nil, fmt.Errorf("%w: diharapkan ',' atau ')'", ErrInvalidStatement)
		}
	}

	if p.pos >= len(p.Tokens) {
		return nil, fmt.Errorf("%w: diharapkan VALUES", ErrUnexpectedEOF)
	}
	if p.Tokens[p.pos].Type != KEYWORD || p.Tokens[p.pos].Literal != "values" {
		return nil, fmt.Errorf("%w: diharapkan VALUES", ErrInvalidStatement)
	}

	p.pos++
	if p.pos >= len(p.Tokens) || p.Tokens[p.pos].Type != LPAREN {
		return nil, fmt.Errorf("%w: diharapkan '(' setelah VALUES", ErrInvalidStatement)
	}
	p.pos++

	var values []string
	for {
		if p.pos >= len(p.Tokens) {
			return nil, fmt.Errorf("%w: diharapkan value atau ')'", ErrUnexpectedEOF)
		}

		token := p.Tokens[p.pos]
		isNull := token.Type == KEYWORD && token.Literal == "null"
		if token.Type != NUMBER && token.Type != STRING && !isNull {
			return nil, fmt.Errorf("%w: value harus berupa string atau null", ErrInvalidStatement)
		}

		values = append(values, token.Literal)
		p.pos++
		if p.pos >= len(p.Tokens) {
			return nil, fmt.Errorf("%w: diharapkan ',' atau ')'", ErrUnexpectedEOF)
		}

		if p.Tokens[p.pos].Type == COMMA {
			p.pos++
			continue
		}
		if p.Tokens[p.pos].Type == RPAREN {
			p.pos++
			break
		}
		return nil, fmt.Errorf("%w: diharapkan ',' atau ')'", ErrInvalidStatement)
	}

	return InsertStatement{
		DBName:  currentDatabase,
		Table:   table,
		Columns: columns,
		Values:  values,
	}, nil
}

// parseDelete "DELETE FROM <table> [WHERE ...]" -- reuse parseWhere() yang
// sama persis dipakai parseSelect, supaya semantik AND/OR dan operator
// perbandingan konsisten di seluruh statement yang punya WHERE.
func (p *Parser) parseDelete() (Statement, error) {
	p.pos++ // lewati DELETE
	if p.pos >= len(p.Tokens) {
		return nil, fmt.Errorf("%w: diharapkan FROM", ErrUnexpectedEOF)
	}
	if p.Tokens[p.pos].Type != KEYWORD || p.Tokens[p.pos].Literal != "from" {
		return nil, fmt.Errorf("%w: diharapkan FROM, tapi dapat: %s", ErrInvalidStatement, p.Tokens[p.pos].Literal)
	}

	p.pos++
	if p.pos >= len(p.Tokens) {
		return nil, fmt.Errorf("%w: diharapkan nama table", ErrUnexpectedEOF)
	}
	if p.Tokens[p.pos].Type != IDENT {
		return nil, fmt.Errorf("%w: diharapkan nama table, tapi dapat: %s", ErrInvalidStatement, p.Tokens[p.pos].Literal)
	}

	table := p.Tokens[p.pos].Literal
	p.pos++

	stmt := DeleteStatement{
		DBName: currentDatabase,
		Table:  table,
	}

	if p.pos < len(p.Tokens) && p.Tokens[p.pos].Type == KEYWORD && p.Tokens[p.pos].Literal == "where" {
		p.pos++
		criteria, err := p.parseWhere()
		if err != nil {
			return nil, err
		}
		stmt.Criteria = criteria
	}

	return stmt, nil
}

func (p *Parser) parseDropDB() (Statement, error) {
	p.pos += 2
	if p.pos >= len(p.Tokens) {
		return nil, fmt.Errorf("%w: diharapkan nama database", ErrUnexpectedEOF)
	}
	var dbName strings.Builder
	for p.pos < len(p.Tokens) {
		token := p.Tokens[p.pos]
		if token.Type != IDENT && token.Type != STRING && token.Type != NUMBER {
			return nil, fmt.Errorf("%w: nama database tidak valid: %s", ErrInvalidStatement, token.Literal)
		}

		if _, err := dbName.WriteString(token.Literal); err != nil {
			return nil, errors.Join(ErrInvalidStatement, err)
		}
		p.pos++
	}

	return DropDatabaseStatement{DBName: dbName.String()}, nil
}

func (p *Parser) parseDropTable() (Statement, error) {
	p.pos += 2
	if p.pos >= len(p.Tokens) {
		return nil, fmt.Errorf("%w: diharapkan nama table", ErrUnexpectedEOF)
	}

	var table strings.Builder
	for p.pos < len(p.Tokens) {
		token := p.Tokens[p.pos]
		if token.Type != IDENT && token.Type != STRING && token.Type != NUMBER {
			return nil, fmt.Errorf("%w: nama database tidak valid: %s", ErrInvalidStatement, token.Literal)
		}

		if _, err := table.WriteString(token.Literal); err != nil {
			return nil, errors.Join(ErrInvalidStatement, err)
		}
		p.pos++
	}

	return DropTableStatement{DBName: currentDatabase, Table: table.String()}, nil

}
