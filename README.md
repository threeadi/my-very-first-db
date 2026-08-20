# 3db

Mini RDBMS in Go, learning journey to build a database from scratch.

## Status

Currently implementing a single-file, single-table B+ tree database with:
- Page-based storage (4096 bytes/page)
- Clustered primary-key B+ tree (leaf + internal pages)
- CRUD: CREATE DATABASE, CREATE TABLE, INSERT, SELECT
- Multi-level tree: leaf split → internal root → traversal root→leaf
- Catalog metadata stored in JSON

## Supported SQL

| Statement | Status |
|-----------|--------|
| `CREATE DATABASE <name>` | ✅ |
| `DROP DATABASE <name>` | ✅ |
| `CREATE TABLE <name> (col TYPE [PRIMARY KEY] [NOT NULL])` | ✅ |
| `DROP TABLE <name>` | ✅ |
| `INSERT INTO <name> VALUES (v1, v2)` | ✅ |
| `SELECT * FROM <name>` | ✅ |
| `SELECT col1, col2 FROM <name>` | ✅ |
| `UPDATE` | ❌ |
| `DELETE` | ❌ |
| `WHERE` filter | ❌ |
| `ORDER BY` | ❌ |
| `SUM` aggregat | ❌ |
| `GROUP BY` | ❌ |

## Quick Start

```bash
go run ./src
# or build and run the binary
```

## Project Structure

```
3db/
├── src/          # Go source code
│   ├── executor.go  # Core logic: insert, select, tree split
│   ├── parser.go    # SQL parser
│   ├── pager.go     # Page I/O abstraction
│   ├── catalog.go   # Metadata JSON
│   ├── config.go    # Configuration
│   ├── lexer.go     # Tokenizer
│   └── main.go      # REPL/CLI
├── data/           # Default data directory
├── go.mod          # Module: 3db
└── README.md       # This file
```

## Development Roadmap

See TODO.md for detailed milestone breakdown.