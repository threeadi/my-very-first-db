package main

import (
	"fmt"
)

type Lexer struct {
	input    string
	position int
}

func NewLexer(input string) *Lexer {
	return &Lexer{
		input:    input,
		position: 0,
	}
}

func (l *Lexer) Tokenize() ([]Token, error) {
	var tokens []Token
	for {
		token, err := l.NextToken()
		if err != nil {
			return nil, err
		}
		if token.Type == EOF {
			break
		}
		tokens = append(tokens, token)
	}

	if len(tokens) <= 0 {
		return nil, ErrUnexpectedEOF
	}

	return tokens, nil
}

func (l *Lexer) NextToken() (Token, error) {
	l.skipWhitespace()
	if l.position >= len(l.input) {
		return Token{
			Type: EOF,
		}, nil
	}

	ch := l.input[l.position]

	var token Token
	switch ch {
	case ',':
		token = Token{Type: COMMA, Literal: ","}
	case '*':
		token = Token{Type: ASTERISK, Literal: "*"}
	case '=', '>', '<', '!':
		isOperator := l.nextPosIsOperator()
		if isOperator {
			token = Token{
				Type:    OPERATOR,
				Literal: string(ch) + string(l.input[l.position+1]),
			}
			l.position++
		} else {
			token = Token{
				Type:    OPERATOR,
				Literal: string(ch),
			}
		}
	case '(':
		token = Token{
			Type:    LPAREN,
			Literal: "(",
		}
	case ')':
		token = Token{
			Type:    RPAREN,
			Literal: ")",
		}
	case ';':
		token = Token{Type: DELIMITER, Literal: ";"}
	case '\'':
		str, err := l.readString(ch)
		if err != nil {
			return Token{}, err
		}
		return Token{Type: STRING, Literal: str}, nil

	default:
		var word string
		if isDigit(ch) {
			word = l.readNumber()
			return Token{
				Type:    NUMBER,
				Literal: word,
			}, nil
		}

		word = l.readIdentifier()
		if word == "" {
			return Token{}, fmt.Errorf("%w : character '%c' at %d", ErrUnexpectedToken, ch, l.position)
		}

		_, exists := Keywords[word]
		if exists {
			return Token{
				Type:    KEYWORD,
				Literal: word,
			}, nil
		}
		return Token{Type: IDENT, Literal: word}, nil

	}
	l.position++

	return token, nil
}

func (l *Lexer) skipWhitespace() {
	for {
		if l.position < len(l.input) && l.input[l.position] == ' ' {
			l.position += 1
			continue
		}
		break
	}
}

func (l *Lexer) nextPosIsOperator() bool {
	nextPos := l.position + 1
	if nextPos >= len(l.input) {
		return false
	}

	ch := l.input[nextPos]
	if ch == '=' || ch == '>' {
		return true
	}
	return false
}

func (l *Lexer) readString(quoteChar byte) (string, error) {
	l.position++
	start := l.position
	var value string

	for l.position < len(l.input) {
		if l.input[l.position] == quoteChar {
			break
		}

		l.position++
		if l.position >= len(l.input) {
			err := fmt.Errorf("%w: ')' not found", ErrSyntax)
			return value, err
		}
	}
	value = l.input[start:l.position]
	l.position++
	return value, nil
}

func (l *Lexer) readNumber() string {
	start := l.position
	for l.position < len(l.input) {
		ch := l.input[l.position]
		if !isDigit(ch) && ch != '.' {
			break
		}
		l.position++
	}
	return l.input[start:l.position]
}

func (l *Lexer) readIdentifier() string {
	start := l.position

	for l.position < len(l.input) {
		ch := l.input[l.position]
		if !isLetter(ch) && !isDigit(ch) {
			break
		}

		l.position++
	}

	return l.input[start:l.position]
}

func isLetter(ch byte) bool {
	result := ch >= 'a' && ch <= 'z' ||
		ch >= 'A' && ch <= 'Z' ||
		ch == '_'

	return result
}

func isDigit(ch byte) bool {
	result := ch >= '0' && ch <= '9'
	return result
}
