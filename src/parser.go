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

type CreateTableStatement struct {
	DBName  string
	Table   string
	Columns []ColumnDef
}

func (CreateTableStatement) statementNode() {}

type SelectStatement struct {
	DBName  string
	Table   string
	Columns []string
}

func (SelectStatement) statementNode() {}

type InsertStatement struct {
	DBName  string
	Table   string
	Columns []string
	Values  []string
}

func (InsertStatement) statementNode() {}

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

	switch p.Tokens[0].Literal {
	case "create":
		if len(p.Tokens) < 2 {
			return nil, ErrUnexpectedEOF
		}

		switch p.Tokens[1].Literal {
		case "database":
			return p.parseCreateDB()
		case "table":
			return p.parseCreateTable()
		default:
			return nil, fmt.Errorf("%w: CREATE %s", ErrInvalidStatement, p.Tokens[1].Literal)
		}

	case "select":
		return p.parseSelect()

	case "insert":
		return p.parseInsert()

	default:
		return nil, fmt.Errorf("%w: %s", ErrInvalidStatement, p.Tokens[0].Literal)
	}
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

		nullable := true
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

		columns = append(columns, ColumnDef{
			Name:         colName,
			ValueType:    valueType,
			Nullable:     nullable,
			DefaultValue: defaultValue,
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

	return SelectStatement{
		DBName:  currentDatabase,
		Table:   table,
		Columns: columns,
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
			return nil, fmt.Errorf("%w: value harus berupa string, number, atau null", ErrInvalidStatement)
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
