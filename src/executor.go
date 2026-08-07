package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type ResultColumn struct {
	Name string
	Type ValueType
}

type Value struct {
	Type  ValueType
	Value any
	Null  bool
}

type Row []Value

type ResultSet struct {
	Columns []ResultColumn
	Rows    []Row
}

type ParsedValue struct {
	ColName string
	Value   any
	Type    ValueType
}

const maxStringLength uint32 = 16 * 1024 * 1024

type Executor struct {
	config  *Config
	catalog *Catalog
}

func NewExecutor(config *Config, catalog *Catalog) *Executor {
	return &Executor{
		config:  config,
		catalog: catalog,
	}
}

func (x *Executor) CreateDatabase(stmt CreateDatabaseStatement) error {
	// 1. Executor TANYA ke catalog: "apa database ini udah ada?"
	if x.catalog.DatabaseExists(stmt.DBName) {
		return fmt.Errorf("%w: %s", ErrDatabaseExists, stmt.DBName)
	}

	_, err := os.Stat(x.config.DataDirectory)
	if err != nil {
		err = os.Mkdir("data", 0755)
		if err != nil {
			log.Fatalf("error %v", err)
		}
	}

	_, err = os.Stat(x.config.DataDirectory + stmt.DBName)
	if err == nil {
		return fmt.Errorf("%w : %s ", ErrDatabaseExists, stmt.DBName)
	}

	err = os.Mkdir(x.config.DataDirectory+stmt.DBName, 0755)
	if err != nil {
		return err
	}
	// 3. Executor MINTA catalog buat mencatat
	x.catalog.RegisterDatabase(stmt.DBName)
	return x.catalog.Save(x.config.CatalogPath)

}

func (x *Executor) CreateTable(stmt CreateTableStatement) error {
	// 1. Executor TANYA ke catalog: "apa database ini udah ada?"
	if !x.catalog.DatabaseExists(stmt.DBName) {
		return ErrDatabaseNotFound
	}

	// 2. Executor TANYA ke catalog: "apa table ini udah ada?"
	if x.catalog.TableExists(stmt.DBName, stmt.Table) {
		return ErrTableExists
	}

	// 3. Executor MEMBUAT file table di folder database
	filePath := filepath.Join(x.config.DataDirectory, fmt.Sprintf("/%s/%s.3tbl", stmt.DBName, stmt.Table))
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}

	var header TableHeader = TableHeader{
		Magic:    [4]byte{'3', 'D', 'B', '1'},
		Version:  1,
		Reserved: [10]byte{},
	}

	buf := new(bytes.Buffer)

	err = binary.Write(buf, binary.LittleEndian, header)
	if err != nil {
		return err
	}

	_, err = file.Write(buf.Bytes())
	if err != nil {
		return err
	}

	err = file.Close()
	if err != nil {
		return err
	}

	// 4. Executor MINTA catalog buat mencatat
	x.catalog.RegisterTable(stmt.DBName, TableDef{
		Name:    stmt.Table,
		Columns: stmt.Columns,
	})

	err = x.catalog.Save(x.config.CatalogPath)
	if err != nil {
		return err
	}

	return nil
}

func (x *Executor) Insert(stmt InsertStatement) error {
	// 1. Cek database aktif ada
	if stmt.DBName == "" {
		return ErrNoDatabaseSelected
	}
	// 2. Cek tabel ada di catalog (RegisterTable sebelumnya)
	hasTable := x.catalog.TableExists(stmt.DBName, stmt.Table)
	if !hasTable {
		return fmt.Errorf("%w : table %s", ErrTableNotFound, stmt.Table)
	}
	// 3. Ambil skema tabel dari catalog (buat tau urutan kolom & tipe data asli)
	columns, err := x.catalog.GetTableColumns(stmt.DBName, stmt.Table)
	if err != nil {
		return fmt.Errorf("%w: columns %s", err, stmt.Columns)
	}
	columnsMap := make(map[string]ColumnDef)
	for _, col := range columns {
		columnsMap[col.Name] = col
	}

	insertColumns := append([]string(nil), stmt.Columns...)
	if len(insertColumns) == 0 {
		// Jika stmt.Columns kosong, berarti user tidak menyebutkan kolom, maka kita asumsikan semua kolom di skema tabel
		for _, col := range columns {
			insertColumns = append(insertColumns, col.Name)
		}
	}

	parsedValues := make([]ParsedValue, len(stmt.Values))

	if len(stmt.Values) != len(insertColumns) {
		return ErrValueCountMismatch
	}

	// 4. Validasi: apa stmt.Columns yang di-INSERT itu match sama kolom yang ada di skema?
	for i, col := range insertColumns {
		colDef, ok := columnsMap[col]
		if !ok {
			return fmt.Errorf("%w. col name %s", ErrColumnNotFound, col)
		}

		value, err := parseValue(stmt.Values[i], colDef.ValueType)
		if err != nil {
			return errors.Join(ErrInvalidDataType, err)
		}

		// validasi: jika kolom nullable = false, maka value tidak boleh NULL
		if colDef.Nullable == false && value == nil {
			return fmt.Errorf("%w. col name : %s", ErrNotNullViolation, col)
		}
		parsedValues[i] = ParsedValue{
			ColName: col,
			Value:   value,
			Type:    colDef.ValueType,
		}
	}

	// 6. Buka file table dengan mode APPEND (bukan Create — filenya udah ada dari CREATE TABLE)
	filePath := filepath.Join(x.config.DataDirectory, fmt.Sprintf("/%s/%s.3tbl", stmt.DBName, stmt.Table))
	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return errors.Join(ErrCorruptTableFile, err)
	}
	defer file.Close()
	_, err = file.Seek(0, io.SeekEnd) // Cursor pindah ke akhir file, biar append
	if err != nil {
		return errors.Join(ErrCorruptTableFile, err)
	}

	// 7. Encode row jadi bytes
	encodedRow, err := encodeRow(columns, parsedValues)
	if err != nil {
		return err
	}

	// 8. Tulis bytes itu ke file
	n, err := file.Write(encodedRow)
	if err != nil {
		return errors.Join(ErrEncodeFailed, err)
	}

	fmt.Println("berhasil nulis row ke file. bytes:", n, "length:", len(encodedRow))
	return nil
}

func (x *Executor) Select(stmt SelectStatement) (ResultSet, error) {
	if stmt.DBName == "" {
		return ResultSet{}, ErrNoDatabaseSelected
	}
	if exists := x.catalog.TableExists(stmt.DBName, stmt.Table); !exists {
		return ResultSet{}, ErrTableNotFound
	}
	columns, err := x.catalog.GetTableColumns(stmt.DBName, stmt.Table)
	if err != nil {
		return ResultSet{}, err
	}
	if len(stmt.Columns) <= 0 {
		return ResultSet{}, ErrColumnNotFound
	}

	var reqColumns map[string]bool = map[string]bool{}
	if stmt.Columns[0] != "*" {
		// check stmt.Columns exists in columns
		for _, col := range stmt.Columns {
			found := false
			for _, expected := range columns {
				if col == expected.Name {
					found = true
					break
				}
			}
			if !found {
				return ResultSet{}, fmt.Errorf("%w. column name :%s", ErrColumnNotFound, col)
			}
			reqColumns[col] = true
		}
	} else {
		stmt.Columns = make([]string, 0, len(columns))
		for _, col := range columns {
			stmt.Columns = append(stmt.Columns, col.Name)
			reqColumns[col.Name] = true
		}
	}

	var resultSet ResultSet = ResultSet{
		Columns: make([]ResultColumn, 0),
		Rows:    make([]Row, 0),
	}
	for _, col := range columns {
		exists := reqColumns[col.Name]
		if exists {
			resultSet.Columns = append(resultSet.Columns, ResultColumn{
				Name: col.Name,
				Type: col.ValueType,
			})
		}
	}

	filePath := filepath.Join(x.config.DataDirectory, fmt.Sprintf("/%s/%s.3tbl", stmt.DBName, stmt.Table))
	file, err := os.Open(filePath)
	if err != nil {
		return ResultSet{}, err
	}
	defer file.Close()

	_, err = file.Seek(15, io.SeekStart) // skip table header (15 bytes)
	if err != nil {
		log.Println("gagal pindah cursor file, error:", err)
		return ResultSet{}, err
	}

	for {
		row, err := decodeRow(file, columns, reqColumns)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return ResultSet{}, err
		}

		if row != nil {
			resultSet.Rows = append(resultSet.Rows, row)
		}
	}

	return resultSet, nil
}

func parseValue(value string, valueType ValueType) (any, error) {
	if strings.EqualFold(value, "NULL") {
		return nil, nil
	}

	switch valueType {
	case VarcharType:
		parsedValue := value
		return parsedValue, nil
	case IntType:
		raw, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("%w: nilai %s tidak valid, harusnya int", ErrInvalidDataType, value)
		}
		parsedValue := int32(raw)
		return parsedValue, nil
	case FloatType:
		raw, err := strconv.ParseFloat(value, 32)
		if err != nil {
			return nil, fmt.Errorf("%w: nilai %s tidak valid, harusnya float", ErrInvalidDataType, value)
		}
		parsedValue := float32(raw)
		return parsedValue, nil

	case BooleanType:
		parsedValue, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("%w: nilai %s tidak valid, harusnya boolean", ErrInvalidDataType, value)
		}
		return parsedValue, nil

	default:
		return nil, ErrInvalidDataType
	}
}

func encodeRow(columns []ColumnDef, values []ParsedValue) ([]byte, error) {
	buf := new(bytes.Buffer)

	rowStatus := true
	/*
		Satu byte dapat menyimpan status NULL untuk 8 kolom.
		1 kolom  = 1 byte
		8 kolom  = 1 byte
		9 kolom  = 2 byte
		16 kolom = 2 byte
		17 kolom = 3 byte
	*/
	nullLength := uint16(math.Ceil(float64(len(columns)) / 8.0))
	nullBitmap := make([]byte, nullLength)

	err := binary.Write(buf, binary.LittleEndian, rowStatus)
	if err != nil {
		log.Default().Println("error encode row header :", err)
		return nil, err
	}

	err = binary.Write(buf, binary.LittleEndian, nullLength)
	if err != nil {
		log.Default().Println("error encode row header :", err)
		return nil, err
	}
	/*
		Ubah []ParsedValue menjadi map supaya tidak perlu
		melakukan nested loop untuk setiap kolom.
	*/
	valuesByColumn := make(map[string]ParsedValue, len(values))
	for _, parsedValue := range values {
		valuesByColumn[parsedValue.ColName] = parsedValue
	}

	orderedValues := make([]any, len(columns))
	for idx, col := range columns {
		parsedValue, exists := valuesByColumn[col.Name]
		if !exists || parsedValue.Value == nil {
			if !col.Nullable {
				return nil, fmt.Errorf("%w, %s ", ErrNotNullViolation, col.Name)
			}

			setNullBit(nullBitmap, idx)
			continue
		}

		orderedValues[idx] = parsedValue.Value
	}

	err = binary.Write(buf, binary.LittleEndian, nullBitmap)
	if err != nil {
		log.Default().Println("error encode row header :", err)
		return nil, err
	}

	// encode each columns
	for idx, col := range columns {
		if isNullBitSet(nullBitmap, idx) {
			// NULL tidak mempunyai payload.
			continue
		}

		var value any
		// Cari value yang sesuai dengan kolom ini
		for _, v := range values {
			if v.ColName == col.Name {
				value = v.Value
				break
			}
		}

		switch col.ValueType {
		case IntType:
			intVal := value.(int32)
			err := binary.Write(buf, binary.LittleEndian, intVal)
			if err != nil {
				return nil, ErrCorruptTableFile
			}
		case VarcharType:
			strVal := value.(string)
			strBytes := []byte(strVal)
			strLen := uint32(len(strBytes))

			// Tulis panjang string dulu (2 bytes == uint16), baru tulis string itu sendiri
			err := binary.Write(buf, binary.LittleEndian, strLen)
			if err != nil {
				return nil, ErrCorruptTableFile
			}

			// Tulis string itu sendiri
			_, err = buf.Write(strBytes)
			if err != nil {
				return nil, ErrCorruptTableFile
			}

		case BooleanType:
			boolVal := value.(bool)
			err := binary.Write(buf, binary.LittleEndian, boolVal)
			if err != nil {
				return nil, ErrCorruptTableFile
			}

		case FloatType:
			floatVal := value.(float32)
			err := binary.Write(buf, binary.LittleEndian, floatVal)
			if err != nil {
				return nil, ErrCorruptTableFile
			}
		}
	}

	return buf.Bytes(), nil
}

func decodeRow(r io.ReadSeeker, cols []ColumnDef, required map[string]bool) (Row, error) {
	// 1. Baca status row.
	var status uint8
	err := binary.Read(r, binary.LittleEndian, &status)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, err
		}
		return nil, ErrCorruptTableFile
	}

	var nullBitmapLength uint16

	err = binary.Read(r, binary.LittleEndian, &nullBitmapLength)
	if err != nil {
		return nil, err
	}
	expectedBitmapLength := (len(cols) + 7) / 8

	if expectedBitmapLength != int(nullBitmapLength) {
		return nil, fmt.Errorf(
			"%w: invalid null bitmap length, expected %d got %d",
			ErrCorruptTableFile,
			expectedBitmapLength,
			nullBitmapLength,
		)
	}

	nullBitmap := make([]byte, nullBitmapLength)
	_, err = io.ReadFull(r, nullBitmap)
	if err != nil {
		return nil, err
	}

	row := make(Row, 0, len(cols))
	for idx, col := range cols {
		var value Value
		if isNullBitSet(nullBitmap, idx) {
			value = Value{
				Type:  col.ValueType,
				Value: nil,
				Null:  true,
			}
			continue
		}

		if required[col.Name] {
			value, err = decodeValue(r, col)
			if err != nil {
				return nil, err
			}

			row = append(row, value)
			continue
		}

		if err := skipValue(r, col); err != nil {
			return nil, err
		}
	}
	return row, nil
}

func skipValue(r io.ReadSeeker, col ColumnDef) error {
	fmt.Println("skipValue called", col)
	switch col.ValueType {
	case IntType, FloatType:
		_, err := r.Seek(4, io.SeekCurrent)
		return err
	case BooleanType:
		_, err := r.Seek(1, io.SeekCurrent)
		return err
	case VarcharType:
		var length uint32
		if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
			return err
		}
		if length > maxStringLength {
			return ErrStringTooLong
		}
		_, err := r.Seek(int64(length), io.SeekCurrent)
		if err != nil {
			return err
		}
	}
	return nil
}

func decodeValue(r io.Reader, col ColumnDef) (Value, error) {
	switch col.ValueType {
	case IntType:
		var value int32
		err := binary.Read(r, binary.LittleEndian, &value)
		if err != nil {
			return Value{}, err
		}

		return Value{Type: IntType, Value: value, Null: false}, nil
	case FloatType:
		var value float32
		err := binary.Read(r, binary.LittleEndian, &value)
		if err != nil {
			return Value{}, err
		}

		return Value{Type: FloatType, Value: value, Null: false}, nil
	case BooleanType:
		var raw uint8

		err := binary.Read(r, binary.LittleEndian, &raw)
		if err != nil {
			return Value{}, err
		}

		if raw != 0 && raw != 1 {
			return Value{}, ErrInvalidValue
		}

		return Value{Type: BooleanType, Value: raw == 1, Null: false}, nil
	case VarcharType:
		var length uint32
		err := binary.Read(r, binary.LittleEndian, &length)
		if err != nil {
			return Value{}, err
		}

		if length > maxStringLength {
			return Value{}, ErrStringTooLong
		}

		var raw []byte = make([]byte, int(length))
		_, err = io.ReadFull(r, raw)

		if err != nil {
			return Value{}, err
		}
		return Value{
			Type:  VarcharType,
			Value: string(raw),
			Null:  false,
		}, nil
	default:
		return Value{}, fmt.Errorf("%w: tipe kolom: %s pada %s", ErrInvalidDataType, col.ValueType, col.Name)
	}
}

func setNullBit(bitmap []byte, columnIndex int) {
	byteIndx := columnIndex / 8
	bitIndx := uint(columnIndex % 8)

	bitmap[byteIndx] |= byte(1 << bitIndx)
}

func isNullBitSet(bitmap []byte, colIdx int) bool {
	byteIdx := colIdx / 8
	bitIdx := uint(colIdx % 8)

	return bitmap[byteIdx]&byte(1<<bitIdx) != 0
}
