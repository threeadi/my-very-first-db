package main

import "errors"

var (
	ErrSyntax           = errors.New("syntax error")
	ErrUnexpectedToken  = errors.New("unexpected token")
	ErrUnexpectedEOF    = errors.New("unexpected end of query")
	ErrInvalidStatement = errors.New("invalid statement")

	ErrNoDatabaseSelected    = errors.New("no database selected")
	ErrDatabaseNotFound      = errors.New("database not found")
	ErrDatabaseExists        = errors.New("database already exists")
	ErrTableNotFound         = errors.New("table not found")
	ErrTableExists           = errors.New("table already exists")
	ErrColumnNotFound        = errors.New("column not found")
	ErrMissingRequiredColumn = errors.New("missing required column")

	ErrValueCountMismatch = errors.New("value count mismatch")
	ErrInvalidValue       = errors.New("invalid value")
	ErrInvalidDataType    = errors.New("invalid data type")
	ErrNotNullViolation   = errors.New("not null constraint violation")
	ErrStringTooLong      = errors.New("string too long")
	ErrValueOutOfRange    = errors.New("value out of range")

	ErrCorruptTableFile = errors.New("corrupt table file")
	ErrPageWriteFailed  = errors.New("failed to write page")
	ErrInternalPageFull = errors.New("internal page is full")
	ErrPageReadFailed   = errors.New("failed to read page")
)
