package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var currentDatabase string

func defaultConfigPath() (string, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("get executable path: %w", err)
	}

	executableDir := filepath.Dir(executablePath)

	return filepath.Join(executableDir, "3db.conf"), nil
}

func main() {
	path, err := defaultConfigPath()
	panicIf(err)
	config, err := LoadConfig(path)
	panicIf(err)
	catalog, err := LoadCatalog(config.CatalogPath)
	panicIf(err)

	executor := NewExecutor(config, catalog)
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("3db > ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		input = strings.ToLower(input)
		keyword, _, _ := strings.Cut(input, " ")

		switch keyword {
		case "use":
			name := strings.TrimSpace(strings.TrimPrefix(input, "use"))
			if !catalog.DatabaseExists(name) {
				fmt.Printf("database %s does not exist\n", name)
				continue
			}
			currentDatabase = name
			fmt.Printf("using database %s\n", currentDatabase)
			continue

		case "clear", "/cls":
			clearScreen()
			continue

		case "exit", ".exit":
			fmt.Println("bye bye")
			return
		}

		lexer := NewLexer(input)
		tokens, err := lexer.Tokenize()
		if err != nil {
			log.Default().Println("lexer error:", err)
			continue
		}

		parser := NewParser(tokens)
		statement, err := parser.Parse()
		if err != nil {
			log.Default().Println("parser error:", err)
			continue
		}

		switch stmt := statement.(type) {
		case CreateDatabaseStatement:
			err = executor.CreateDatabase(stmt)
			if err != nil {
				log.Default().Println("error create database:", err)
				continue
			}
			currentDatabase = stmt.DBName
			log.Default().Printf("create database %s success\n", stmt.DBName)

		case CreateTableStatement:
			err = executor.CreateTable(stmt)
			if err != nil {
				log.Default().Println("error execute create table:", err)
				continue
			}
			log.Default().Printf("create table %s success\n", stmt.Table)

		case InsertStatement:
			err = executor.Insert(stmt)
			if err != nil {
				log.Default().Printf("error insert: %v\n", err)
			}

		case SelectStatement:
			result, selectErr := executor.Select(stmt)
			if selectErr != nil {
				log.Default().Println("error select:", selectErr)
				continue
			}
			printResultSet(result)

		default:
			log.Default().Printf("unsupported statement type %T\n", statement)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Default().Println("scanner error:", err)
	}
}

func clearScreen() {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "cls")
	} else {
		cmd = exec.Command("clear")
	}

	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		log.Default().Println("clear screen error:", err)
	}
}

func printResultSet(result ResultSet) {
	if len(result.Columns) == 0 {
		fmt.Println("table kosong")
		return
	}

	// Lebar awal berdasarkan nama kolom.
	widths := make([]int, len(result.Columns))
	for i, col := range result.Columns {
		widths[i] = len(col.Name)
	}

	for _, record := range result.Records {
		for i, column := range record {
			if i >= len(widths) {
				break
			}

			value := columnDisplayValue(column)
			if len(value) > widths[i] {
				widths[i] = len(value)
			}
		}
	}

	printSeparator(widths)

	// Header.
	fmt.Print("|")
	for i, col := range result.Columns {
		fmt.Printf(" %-*s |", widths[i], col.Name)
	}
	fmt.Println()

	printSeparator(widths)

	if len(result.Records) == 0 {
		fmt.Println("tidak ada data")
		printSeparator(widths)
		return
	}

	// Rows.
	for _, row := range result.Records {
		fmt.Print("|")

		for i := range result.Columns {
			value := "NULL"
			isNull := true

			if i < len(row) {
				column := row[i]
				isNull = column.Null || column.Value == nil
				value = columnDisplayValue(column)
			}

			if isNull {
				padding := widths[i] - len(value)
				fmt.Printf(" \033[3m%s\033[23m%s |", value, strings.Repeat(" ", padding))
			} else {
				fmt.Printf(" %-*s |", widths[i], value)
			}
		}

		fmt.Println()
	}

	printSeparator(widths)
}

func columnDisplayValue(column Value) string {
	if column.Null || column.Value == nil {
		return "NULL"
	}

	return fmt.Sprint(column.Value)
}

func printSeparator(widths []int) {
	fmt.Print("+")

	for _, width := range widths {
		fmt.Print(strings.Repeat("-", width+2))
		fmt.Print("+")
	}

	fmt.Println()
}

func panicIf(err error) {
	if err != nil {
		panic(err)
	}
}
