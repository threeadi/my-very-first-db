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
	"sort"
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

type Record []Value

type ResultSet struct {
	Columns []ResultColumn
	Records []Record
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

	pkFound := false
	for _, col := range stmt.Columns {
		if !pkFound && col.Primary {
			pkFound = true
			continue
		}
		if pkFound && col.Primary {
			return errors.New("PRIMARY KEY hanya support 1")
		}
	}

	if !pkFound {
		return errors.New("table harus memiliki minimal 1 PK")
	}

	// 3. Executor MEMBUAT file table di folder database
	filePath := filepath.Join(x.config.DataDirectory, fmt.Sprintf("/%s/%s.3tbl", stmt.DBName, stmt.Table))
	pager, err := CreatePager(filePath)
	if err != nil {
		return err
	}
	defer pager.Close()

	meta := MetaPage{
		Magic:      [4]byte{'3', 'D', 'B', '1'},
		Version:    1,
		PageSize:   PageSize,
		RootPageID: 1,
		NextPageID: 2,
	}

	page := EncodeMetaPage(meta)

	err = pager.WritePage(page)
	if err != nil {
		return err
	}

	rootLeaf, err := pager.AllocatePage()
	if err != nil {
		return err
	}

	h := IndexPageHeader{
		PageType:          PageTypeLeaf,
		PageID:            rootLeaf.ID,
		ParentID:          InvalidPageID,
		Level:             0,
		RecordCount:       0,
		FirstRecordOffset: 0,
		FreeStart:         IndexPageHeaderSize,
	}

	EncodeIndexPageHeader(rootLeaf, h)

	err = pager.WritePage(rootLeaf)
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

	pkColIdx := -1
	var pkName string

	for i, col := range columns {
		if col.Primary {
			pkColIdx = i
			pkName = col.Name
			break
		}
	}

	if pkColIdx == -1 {
		return errors.New("primary key tidak ditemukan")
	}

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
	var newPK int32
	foundPK := false
	for _, parsed := range parsedValues {
		if parsed.ColName != pkName {
			continue
		}

		value, ok := parsed.Value.(int32)
		if !ok {
			return errors.New("sementara primary key hanya support INT")
		}

		newPK = value
		foundPK = true
		break
	}

	if !foundPK {
		return errors.New("primary key harus diisi")
	}

	// Buka file table dengan mode APPEND (bukan Create — filenya udah ada dari CREATE TABLE)
	filePath := filepath.Join(x.config.DataDirectory, fmt.Sprintf("/%s/%s.3tbl", stmt.DBName, stmt.Table))
	pager, err := OpenPager(filePath)
	if err != nil {
		return err
	}
	defer pager.Close()

	metaRaw, err := pager.ReadPage(0)
	if err != nil {
		return err
	}

	meta, err := DecodeMetaPage(metaRaw)
	if err != nil {
		return err
	}

	rootPage, err := pager.ReadPage(meta.RootPageID)
	if err != nil {
		return err
	}

	page, err := x.targetPage(pager, rootPage, newPK)
	if err != nil {
		return err
	}
	head, err := DecodeIndexPageHeader(page)
	if err != nil {
		return err
	}

	// cari PK column + new PK
	var prevOffset uint16
	currentOffset := head.FirstRecordOffset
	for currentOffset != 0 {
		record, _, nextOffset, _, err := decodeRecord(columns, page.Data[currentOffset:])
		if err != nil {
			return err
		}
		currentPK, ok := record[pkColIdx].Value.(int32)
		if !ok {
			return ErrInvalidDataType
		}

		if newPK == currentPK {
			return fmt.Errorf("duplicate primary key: %d", newPK)
		}
		if newPK < currentPK {
			break
		}
		prevOffset = currentOffset
		currentOffset = nextOffset
	}

	encodedRecord, err := encodeRecord(columns, parsedValues, currentOffset)
	if err != nil {
		return err
	}

	if IndexPageHeaderSize+len(encodedRecord) > int(PageSize) {
		return ErrValueOutOfRange
	}

	recordOffset := head.FreeStart
	recordEnd := int(recordOffset) + len(encodedRecord)

	if recordEnd > int(PageSize) {
		// splitRootLeaf HANYA boleh untuk tree yang root-nya masih leaf.
		rootHead, err := DecodeIndexPageHeader(rootPage)
		if err != nil {
			return err
		}
		if rootHead.PageType != PageTypeLeaf {
			return errors.New("split non-root leaf belum diimplementasikan")
		}

		return x.splitRootLeaf(pager, page, columns, pkColIdx, newPK, parsedValues)
	}

	copy(page.Data[int(recordOffset):recordEnd], encodedRecord)
	if prevOffset == 0 {
		head.FirstRecordOffset = recordOffset
	} else {
		nextOffsetPos := int(prevOffset) + recordNextOffsetPosition(columns)

		binary.LittleEndian.PutUint16(
			page.Data[nextOffsetPos:nextOffsetPos+2],
			recordOffset,
		)
	}

	head.RecordCount++
	head.FreeStart = uint16(recordEnd)

	EncodeIndexPageHeader(page, head)
	if err := pager.WritePage(page); err != nil {
		return err
	}

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

	var resultSet ResultSet = ResultSet{
		Columns: make([]ResultColumn, 0),
		Records: make([]Record, 0),
	}
	for _, col := range columns {
		resultSet.Columns = append(resultSet.Columns, ResultColumn{
			Name: col.Name,
			Type: col.ValueType,
		})
	}

	filePath := filepath.Join(x.config.DataDirectory, fmt.Sprintf("/%s/%s.3tbl", stmt.DBName, stmt.Table))
	pager, err := OpenPager(filePath)
	if err != nil {
		return ResultSet{}, err
	}
	defer pager.Close()
	page, err := pager.ReadPage(PageID(0))
	if err != nil {
		return ResultSet{}, err
	}
	meta, err := DecodeMetaPage(page)
	if err != nil {
		return ResultSet{}, err
	}

	rootPage, err := pager.ReadPage(meta.RootPageID)
	if err != nil {
		return ResultSet{}, err
	}

	records, err := x.scanTree(pager, rootPage, columns)
	if err != nil {
		return ResultSet{}, err
	}

	resultSet.Records = records

	return resultSet, nil
}

func (x *Executor) scanTree(pager *Pager, page *Page, columns []ColumnDef) ([]Record, error) {
	head, err := DecodeIndexPageHeader(page)
	if err != nil {
		return nil, err
	}

	switch head.PageType {
	case PageTypeLeaf:
		return x.scanLeaf(page, columns)

	case PageTypeInternal:
		offset := IndexPageHeaderSize

		leftPageID := PageID(
			binary.LittleEndian.Uint32(
				page.Data[offset : offset+4],
			),
		)

		offset += 4

		offset += 4

		rightPageID := PageID(
			binary.LittleEndian.Uint32(
				page.Data[offset : offset+4],
			),
		)

		leftPage, err := pager.ReadPage(leftPageID)
		if err != nil {
			return nil, err
		}

		rightPage, err := pager.ReadPage(rightPageID)
		if err != nil {
			return nil, err
		}

		leftRecords, err := x.scanTree(pager, leftPage, columns)
		if err != nil {
			return nil, err
		}

		rightRecords, err := x.scanTree(pager, rightPage, columns)
		if err != nil {
			return nil, err
		}

		return append(leftRecords, rightRecords...), nil

	default:
		return nil, ErrCorruptTableFile
	}
}

func (x *Executor) scanLeaf(page *Page, columns []ColumnDef) ([]Record, error) {
	rootHead, err := DecodeIndexPageHeader(page)
	if err != nil {
		return nil, err
	}

	recordOffset := rootHead.FirstRecordOffset
	var records []Record

	for recordOffset != 0 {
		record, _, nextOffset, _, err := decodeRecord(columns, page.Data[recordOffset:])
		if err != nil {
			return nil, err
		}

		records = append(records, record)
		recordOffset = nextOffset
	}

	return records, nil
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

// ┌──────────────────────────────────────┐
// │ VarLen metadata                      │
// │   uint16 per VARCHAR column          │
// ├──────────────────────────────────────┤
// │ NULL bitmap                          │
// │   hanya untuk nullable columns       │
// ├──────────────────────────────────────┤
// │ Flags                  uint8         │
// ├──────────────────────────────────────┤
// │ NextOffset             uint16        │
// ├──────────────────────────────────────┤
// │ Column Data                          │
// │   INT       4 bytes                  │
// │   VARCHAR   raw bytes                │
// │   FLOAT     4 bytes                  │
// │   BOOLEAN   1 byte                   │
// │   NULL      0 byte                   │
// └──────────────────────────────────────┘
func encodeRecord(columns []ColumnDef, values []ParsedValue, nextOff uint16) ([]byte, error) {
	buf := new(bytes.Buffer)

	/**
	* Null bitmap hanya digunakan dibuat untuk nullable column saja, jangan semua kolom
	**/
	nullable := 0
	for _, col := range columns {
		if col.Nullable {
			nullable++
		}
	}

	nullBitmap := make([]byte, (nullable+7)/8)

	/*
		Ubah []ParsedValue menjadi map supaya tidak perlu
		melakukan nested loop untuk setiap kolom.
	*/
	valuesByColumn := make(map[string]ParsedValue, len(values))
	for _, parsedValue := range values {
		valuesByColumn[parsedValue.ColName] = parsedValue
	}

	orderedValues := make([]any, len(columns))
	nullIdx := 0

	for idx, col := range columns {
		parsedValue, exists := valuesByColumn[col.Name]
		isNull := !exists || parsedValue.Value == nil

		if isNull {
			if !col.Nullable {
				return nil, fmt.Errorf(
					"%w, %s",
					ErrNotNullViolation,
					col.Name,
				)
			}

			setNullBit(nullBitmap, nullIdx)
		} else {
			orderedValues[idx] = parsedValue.Value
		}

		if col.Nullable {
			nullIdx++
		}
	}

	for colIdx, col := range columns {
		if col.ValueType != VarcharType {
			continue
		}

		var length uint16
		if orderedValues[colIdx] != nil {
			value, ok := orderedValues[colIdx].(string)
			if !ok {
				return nil, ErrInvalidDataType
			}
			raw := []byte(value)

			if len(raw) > 0xffff {
				return nil, ErrStringTooLong
			}

			length = uint16(len(raw))
		}

		if err := binary.Write(buf, binary.LittleEndian, length); err != nil {
			return nil, err
		}
	}

	_, err := buf.Write(nullBitmap)
	if err != nil {
		return nil, err
	}
	// ---------------------------------------------------------
	// 6. Flags
	//
	// bit 0 nanti bisa digunakan sebagai delete-mark.
	//
	// 00000000 = normal
	// ---------------------------------------------------------
	var flags uint8 = 0
	if err := binary.Write(buf, binary.LittleEndian, flags); err != nil {
		return nil, err
	}

	// Next record offset
	if err := binary.Write(buf, binary.LittleEndian, nextOff); err != nil {
		return nil, err
	}

	// encode each columns
	for idx, col := range columns {
		value := orderedValues[idx]

		if value == nil {
			continue
		}

		switch col.ValueType {
		case IntType:
			intVal, ok := value.(int32)
			if !ok {
				return nil, ErrInvalidDataType
			}

			err := binary.Write(buf, binary.LittleEndian, intVal)
			if err != nil {
				return nil, ErrCorruptTableFile
			}
		case FloatType:
			floatVal, ok := value.(float32)
			if !ok {
				return nil, ErrInvalidDataType
			}

			if err := binary.Write(buf, binary.LittleEndian, floatVal); err != nil {
				return nil, ErrCorruptTableFile
			}
		case BooleanType:
			boolVal := value.(bool)
			err := binary.Write(buf, binary.LittleEndian, boolVal)
			if err != nil {
				return nil, ErrCorruptTableFile
			}
		case VarcharType:
			strVal, ok := value.(string)
			if !ok {
				return nil, ErrInvalidDataType
			}

			if _, err = buf.Write([]byte(strVal)); err != nil {
				return nil, ErrCorruptTableFile
			}
		}
	}

	return buf.Bytes(), nil
}

func decodeRecord(columns []ColumnDef, data []byte) (record Record, flag uint8, nextOffset uint16, recordSize int, err error) {
	offset := 0

	// -----------------------------------------
	// 1. Baca metadata panjang VARCHAR
	// -----------------------------------------
	varcharLengths := make(map[int]uint16)
	for colIdx, col := range columns {
		if col.ValueType != VarcharType {
			continue
		}

		if offset+2 > len(data) {
			return nil, 0, 0, 0, errors.New("invalid record: varchar metadata overflow")
		}

		length := binary.LittleEndian.Uint16(data[offset : offset+2])
		offset += 2

		varcharLengths[colIdx] = length
	}
	nullableCount := 0

	for _, col := range columns {
		if col.Nullable {
			nullableCount++
		}
	}

	nullBitmapLength := (nullableCount + 7) / 8
	if offset+nullBitmapLength > len(data) {
		return nil, 0, 0, 0, errors.New("invalid record: null bitmap overflow")
	}
	nullBitmap := data[offset : offset+nullBitmapLength]
	offset += nullBitmapLength

	// -----------------------------------------
	// 3. flags
	// -----------------------------------------
	if offset+1 > len(data) {
		return nil, 0, 0, 0, fmt.Errorf("invalid record: missing flags")
	}

	flags := data[offset]
	offset++

	// Next Offset (2 bytes)
	nextOffset = binary.LittleEndian.Uint16(data[offset : offset+2])
	offset += 2

	// -----------------------------------------
	// 5. Decode column payload
	// -----------------------------------------
	record = make(Record, len(columns))

	nullableIdx := 0
	for colIdx, col := range columns {
		isNull := false
		if col.Nullable {
			isNull = isNullBitSet(nullBitmap, nullableIdx)
			nullableIdx++
		}
		if isNull {
			record[colIdx] = Value{
				Type:  col.ValueType,
				Null:  true,
				Value: nil,
			}
			continue
		}
		switch col.ValueType {
		case IntType:
			if offset+4 > len(data) {
				return nil, 0, 0, 0, ErrInvalidValue
			}

			value := int32(binary.LittleEndian.Uint32(data[offset : offset+4]))

			record[colIdx] = Value{
				Type:  IntType,
				Value: value,
				Null:  false,
			}
			offset += 4
		case FloatType:
			if offset+4 > len(data) {
				return nil, 0, 0, 0, ErrInvalidValue
			}
			raw := binary.LittleEndian.Uint32(data[offset : offset+4])
			record[colIdx] = Value{
				Type:  FloatType,
				Value: math.Float32frombits(raw),
				Null:  false,
			}
			offset += 4

		case BooleanType:
			if offset+1 > len(data) {
				return nil, 0, 0, 0, fmt.Errorf(
					"invalid record: boolean overflow column %s",
					col.Name,
				)
			}

			record[colIdx] = Value{
				Type:  BooleanType,
				Value: data[offset] != 0,
				Null:  false,
			}
			offset++
		case VarcharType:
			length := int(varcharLengths[colIdx])
			if offset+length > len(data) {
				return nil, 0, 0, 0, ErrInvalidValue
			}
			record[colIdx] = Value{
				Type:  VarcharType,
				Value: string(data[offset : offset+length]),
				Null:  false,
			}
			offset += length
		default:
			return nil, 0, 0, 0, fmt.Errorf(
				"unsupported value type for column %s",
				col.Name,
			)
		}
	}
	return record, flags, nextOffset, offset, nil
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

func setNullBit(bitmap []byte, nullIdx int) {
	byteIndx := nullIdx / 8
	bitIndx := uint(nullIdx % 8)

	bitmap[byteIndx] |= byte(1 << bitIndx)
}

func isNullBitSet(bitmap []byte, colIdx int) bool {
	byteIdx := colIdx / 8
	bitIdx := colIdx % 8

	return bitmap[byteIdx]&byte(1<<bitIdx) != 0
}

func EncodeMetaPage(meta MetaPage) *Page {
	page := Page{
		ID:   0,
		Data: [PageSize]byte{},
	}
	copy(page.Data[0:4], meta.Magic[:])

	page.Data[4] = meta.Version

	binary.LittleEndian.PutUint16(
		page.Data[5:7],
		meta.PageSize,
	)

	binary.LittleEndian.PutUint32(
		page.Data[7:11],
		uint32(meta.RootPageID),
	)

	binary.LittleEndian.PutUint32(
		page.Data[11:15],
		uint32(meta.NextPageID),
	)
	return &page
}

func DecodeMetaPage(page *Page) (MetaPage, error) {
	meta := MetaPage{}
	n := copy(meta.Magic[:], page.Data[0:4])
	if n != 4 {
		return meta, ErrPageReadFailed
	}

	_, err := binary.Decode(page.Data[4:5], binary.LittleEndian, &meta.Version)
	if err != nil {
		return meta, err
	}
	_, err = binary.Decode(page.Data[5:7], binary.LittleEndian, &meta.PageSize)
	if err != nil {
		return meta, err
	}

	_, err = binary.Decode(page.Data[7:11], binary.LittleEndian, &meta.RootPageID)
	if err != nil {
		return meta, err
	}

	_, err = binary.Decode(page.Data[11:15], binary.LittleEndian, &meta.NextPageID)
	if err != nil {
		return meta, err
	}
	return meta, nil
}

func EncodeIndexPageHeader(page *Page, h IndexPageHeader) {
	page.Data[0] = byte(h.PageType)
	binary.LittleEndian.PutUint32(page.Data[1:5], uint32(h.PageID))

	binary.LittleEndian.PutUint32(page.Data[5:9], uint32(h.ParentID))

	binary.LittleEndian.PutUint16(page.Data[9:11], h.Level)

	binary.LittleEndian.PutUint16(page.Data[11:13], h.RecordCount)

	binary.LittleEndian.PutUint16(page.Data[13:15], h.FirstRecordOffset)

	binary.LittleEndian.PutUint16(page.Data[15:17], h.FreeStart)
}

func DecodeIndexPageHeader(page *Page) (IndexPageHeader, error) {
	var h IndexPageHeader = IndexPageHeader{}
	h.PageType = PageType(page.Data[0])
	_, err := binary.Decode(page.Data[1:5], binary.LittleEndian, &h.PageID)
	if err != nil {
		return h, err
	}
	_, err = binary.Decode(page.Data[5:9], binary.LittleEndian, &h.ParentID)
	if err != nil {
		return h, err
	}
	_, err = binary.Decode(page.Data[9:11], binary.LittleEndian, &h.Level)
	if err != nil {
		return h, err
	}

	_, err = binary.Decode(page.Data[11:13], binary.LittleEndian, &h.RecordCount)
	if err != nil {
		return h, err
	}

	_, err = binary.Decode(page.Data[13:15], binary.LittleEndian, &h.FirstRecordOffset)
	if err != nil {
		return h, err
	}

	_, err = binary.Decode(page.Data[15:17], binary.LittleEndian, &h.FreeStart)
	if err != nil {
		return h, err
	}
	return h, nil
}

func recordNextOffsetPosition(cols []ColumnDef) int {
	varcharCnt := 0
	nullableCnt := 0

	for _, col := range cols {
		if col.ValueType == VarcharType {
			varcharCnt++
		}
		if col.Nullable {
			nullableCnt++
		}
	}
	varlenMetadataSize := varcharCnt * 2
	nullBitmapSize := (nullableCnt + 7) / 8
	flagSize := 1

	return varlenMetadataSize + nullBitmapSize + flagSize
}

func (x *Executor) splitRootLeaf(
	pager *Pager,
	rootPage *Page,
	columns []ColumnDef,
	pkColIdx int,
	newPK int32,
	parsedValues []ParsedValue,
) error {
	records, err := readLeafRecords(
		rootPage,
		columns,
		pkColIdx,
	)
	if err != nil {
		return err
	}

	newRecordData, err := encodeRecord(
		columns,
		parsedValues,
		0,
	)
	if err != nil {
		return err
	}

	records = append(records, LeafRecord{
		PK:   newPK,
		Data: newRecordData,
	})

	sort.Slice(records, func(i, j int) bool {
		return records[i].PK < records[j].PK
	})

	if len(records) < 2 {
		return errors.New("tidak cukup record untuk split")
	}

	middle := len(records) / 2

	leftRecords := records[:middle]
	rightRecords := records[middle:]

	if len(leftRecords) == 0 || len(rightRecords) == 0 {
		return errors.New("invalid leaf split")
	}

	// =====================================================
	// PAGE 1 = LEFT LEAF
	// =====================================================

	if err := rewriteLeaf(
		rootPage,
		leftRecords,
		columns,
	); err != nil {
		return err
	}

	// =====================================================
	// PAGE 2 = RIGHT LEAF
	// =====================================================

	rightPage, err := pager.AllocatePage()
	if err != nil {
		return err
	}

	rightHeader := IndexPageHeader{
		PageType:          PageTypeLeaf,
		PageID:            rightPage.ID,
		ParentID:          InvalidPageID, // sementara
		Level:             0,
		RecordCount:       0,
		FirstRecordOffset: 0,
		FreeStart:         IndexPageHeaderSize,
	}

	EncodeIndexPageHeader(rightPage, rightHeader)

	if err := rewriteLeaf(
		rightPage,
		rightRecords,
		columns,
	); err != nil {
		return err
	}

	// IMPORTANT:
	// write Page2 dulu supaya file membesar.
	if err := pager.WritePage(rightPage); err != nil {
		return err
	}

	// =====================================================
	// PAGE 3 = NEW INTERNAL ROOT
	// =====================================================

	parentPage, err := pager.AllocatePage()
	if err != nil {
		return err
	}

	separatorKey := rightRecords[0].PK

	headerParent := IndexPageHeader{
		PageType:          PageTypeInternal,
		PageID:            parentPage.ID,
		ParentID:          InvalidPageID,
		Level:             1,
		RecordCount:       1,
		FirstRecordOffset: 0,

		// Header 17
		// + FirstChild 4
		// + separator 4
		// + RightChild 4
		FreeStart: IndexPageHeaderSize + 12,
	}

	EncodeIndexPageHeader(parentPage, headerParent)

	offset := IndexPageHeaderSize

	// FirstChildPageID = left leaf
	binary.LittleEndian.PutUint32(
		parentPage.Data[offset:offset+4],
		uint32(rootPage.ID),
	)
	offset += 4

	// separator key
	binary.LittleEndian.PutUint32(
		parentPage.Data[offset:offset+4],
		uint32(separatorKey),
	)
	offset += 4

	// RightChildPageID = right leaf
	binary.LittleEndian.PutUint32(
		parentPage.Data[offset:offset+4],
		uint32(rightPage.ID),
	)

	// =====================================================
	// Kedua leaf sekarang punya parent = Page3
	// =====================================================

	leftHeader, err := DecodeIndexPageHeader(rootPage)
	if err != nil {
		return err
	}

	leftHeader.ParentID = parentPage.ID
	leftHeader.Level = 0

	EncodeIndexPageHeader(rootPage, leftHeader)

	rightHeader, err = DecodeIndexPageHeader(rightPage)
	if err != nil {
		return err
	}

	rightHeader.ParentID = parentPage.ID
	rightHeader.Level = 0

	EncodeIndexPageHeader(rightPage, rightHeader)

	// =====================================================
	// Persist pages
	// =====================================================

	if err := pager.WritePage(rootPage); err != nil {
		return err
	}

	if err := pager.WritePage(rightPage); err != nil {
		return err
	}

	if err := pager.WritePage(parentPage); err != nil {
		return err
	}

	// =====================================================
	// Meta sekarang menunjuk INTERNAL ROOT baru
	// =====================================================

	metaPage, err := pager.ReadPage(0)
	if err != nil {
		return err
	}

	meta, err := DecodeMetaPage(metaPage)
	if err != nil {
		return err
	}

	meta.RootPageID = parentPage.ID

	metaPage = EncodeMetaPage(meta)

	if err := pager.WritePage(metaPage); err != nil {
		return err
	}

	return nil
}

type LeafRecord struct {
	PK   int32
	Data []byte
}

func readLeafRecords(
	page *Page,
	columns []ColumnDef,
	pkColIdx int,
) ([]LeafRecord, error) {
	head, err := DecodeIndexPageHeader(page)
	if err != nil {
		return nil, err
	}

	records := make([]LeafRecord, 0, head.RecordCount)
	currentOffset := head.FirstRecordOffset

	for currentOffset != 0 {
		// 1. Decode record untuk mengetahui:
		//    - nilai PK
		//    - NextOffset
		//    - ukuran record
		record, _, nextOffset, recordSize, err := decodeRecord(
			columns,
			page.Data[currentOffset:],
		)
		if err != nil {
			return nil, err
		}
		pk, ok := record[pkColIdx].Value.(int32)
		if !ok {
			return nil, ErrInvalidDataType
		}
		endRecord := int(currentOffset) + recordSize
		if endRecord > int(PageSize) {
			return nil, ErrCorruptTableFile
		}
		raw := make([]byte, recordSize)
		copy(raw, page.Data[int(currentOffset):endRecord])

		records = append(records, LeafRecord{
			PK:   pk,
			Data: raw,
		})

		currentOffset = nextOffset
	}

	return records, nil
}

// 1. decode IndexPageHeader
// 2. kosongkan area record page
// 3. reset:
//    RecordCount = 0
//    FirstRecordOffset = 0
//    FreeStart = 17

// 4. loop records
//    ↓
//    tulis record ke FreeStart
//    ↓
//    patch NextOffset record sebelumnya
//    ↓
//    advance FreeStart

// 5. record terakhir:
//    NextOffset = 0

// 6. encode header kembali
func rewriteLeaf(page *Page, records []LeafRecord, columns []ColumnDef) error {
	header, err := DecodeIndexPageHeader(page)
	if err != nil {
		return err
	}
	page.Data = [PageSize]byte{}
	header.RecordCount = 0
	header.FirstRecordOffset = 0
	header.FreeStart = IndexPageHeaderSize

	// Empty leaf valid.
	if len(records) == 0 {
		EncodeIndexPageHeader(page, header)
		return nil
	}

	offsets := make([]uint16, len(records))
	currentOffset := uint16(IndexPageHeaderSize)
	for i, r := range records {
		recordEnd := int(currentOffset) + len(r.Data)
		if recordEnd > int(PageSize) {
			return errors.New("record tidak muat di leaf page")
		}

		offsets[i] = currentOffset
		currentOffset = uint16(recordEnd)
	}

	header.FirstRecordOffset = offsets[0]
	nextOffsetPos := recordNextOffsetPosition(columns)

	for i, r := range records {
		rOffset := offsets[i]
		recordEnd := int(rOffset) + len(r.Data)

		copy(page.Data[rOffset:recordEnd], r.Data)

		var nextOffset uint16

		if i+1 < len(records) {
			nextOffset = offsets[i+1]
		}

		pos := int(rOffset) + nextOffsetPos
		binary.LittleEndian.PutUint16(
			page.Data[pos:pos+2],
			nextOffset,
		)

		header.RecordCount++
	}
	header.FreeStart = currentOffset
	EncodeIndexPageHeader(page, header)
	return nil
}

func (x *Executor) targetPage(pager *Pager, parent *Page, newPK int32) (*Page, error) {
	head, err := DecodeIndexPageHeader(parent)
	if err != nil {
		return nil, err
	}

	if head.PageType == PageTypeLeaf {
		return parent, nil
	}

	if head.PageType != PageTypeInternal {
		return nil, ErrCorruptTableFile
	}

	offset := IndexPageHeaderSize

	// Left child
	var leftPageID uint32

	_, err = binary.Decode(
		parent.Data[offset:offset+4],
		binary.LittleEndian,
		&leftPageID,
	)
	if err != nil {
		return nil, err
	}

	offset += 4

	var separator int32

	_, err = binary.Decode(parent.Data[offset:offset+4], binary.LittleEndian, &separator)
	if err != nil {
		return nil, err
	}

	offset += 4

	var rightPageID uint32

	_, err = binary.Decode(
		parent.Data[offset:offset+4],
		binary.LittleEndian,
		&rightPageID,
	)
	if err != nil {
		return nil, err
	}

	var childID PageID

	if newPK < separator {
		childID = PageID(leftPageID)
	} else {
		childID = PageID(rightPageID)
	}

	child, err := pager.ReadPage(childID)
	if err != nil {
		return nil, err
	}

	return x.targetPage(pager, child, newPK)
}
