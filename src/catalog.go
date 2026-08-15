package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
)

type Catalog struct {
	Databases map[string]Database `json:"databases"`
}

type ValueType string

const (
	VarcharType ValueType = "varchar"
	IntType     ValueType = "int"
	BooleanType ValueType = "boolean"
	FloatType   ValueType = "float"
)

type ColumnDef struct {
	Name         string    `json:"name"`
	ValueType    ValueType `json:"value_type"`
	Primary      bool      `json:"pk"`
	Nullable     bool      `json:"nullable"`
	DefaultValue any       `json:"default_value"`
}

type TableDef struct {
	Name    string      `json:"name"`
	Columns []ColumnDef `json:"columns"`
}

type Database struct {
	Name   string              `json:"name"`
	Tables map[string]TableDef `json:"tables"`
}

func NewCatalog() *Catalog {
	return &Catalog{
		Databases: make(map[string]Database),
	}
}

func LoadCatalog(path string) (*Catalog, error) {
	file, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		catalog := NewCatalog()

		return catalog, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read catalog : %w", err)
	}

	catalog := NewCatalog()

	if err = json.Unmarshal(file, catalog); err != nil {
		return nil, fmt.Errorf("decode catalog : %w", err)
	}

	if catalog.Databases == nil {
		catalog.Databases = make(map[string]Database)
	}

	for dbName, db := range catalog.Databases {
		if db.Tables == nil {
			db.Tables = make(map[string]TableDef)
			catalog.Databases[dbName] = db
		}
	}

	return catalog, nil
}

func (c *Catalog) DatabaseExists(name string) bool {
	_, exists := c.Databases[name]
	return exists
}

func (c *Catalog) RegisterDatabase(name string) error {
	if name == "" {
		return errors.New("database name cannot be empty")
	}

	if c.Databases == nil {
		c.Databases = make(map[string]Database)
	}

	if c.DatabaseExists(name) {
		return fmt.Errorf("%w: %s", ErrDatabaseExists, name)
	}

	c.Databases[name] = Database{Name: name, Tables: make(map[string]TableDef)}
	return nil
}

func (c *Catalog) ListDatabases() []string {
	var dbs []string
	for db := range c.Databases {
		dbs = append(dbs, db)
	}
	return dbs
}

func (c *Catalog) TableExists(dbName, tableName string) bool {
	db, exists := c.Databases[dbName]
	if !exists {
		return false
	}
	_, exists = db.Tables[tableName]
	return exists
}

func (c *Catalog) RegisterTable(dbName string, table TableDef) {
	db, exists := c.Databases[dbName]
	if !exists {
		log.Default().Printf("database %s does not exist", dbName)
		return
	}
	db.Tables[table.Name] = table
}

func (c *Catalog) GetTableColumns(dbName, tableName string) ([]ColumnDef, error) {
	db, exists := c.Databases[dbName]
	if !exists {
		return nil, ErrDatabaseNotFound
	}

	table, exists := db.Tables[tableName]
	if !exists {
		return nil, ErrTableNotFound
	}
	return table.Columns, nil
}

func (c *Catalog) Save(path string) error {
	data, err := json.Marshal(c)
	if err != nil {
		log.Default().Println("error marshalling catalog:", err)
		return err
	}

	err = os.WriteFile(path, data, 0644)
	if err != nil {
		log.Default().Println("error writing catalog file:", err)
		return err
	}
	return nil
}
