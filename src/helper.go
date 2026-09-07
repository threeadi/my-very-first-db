package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

const MAX_STRING_LENGTH uint32 = 16 * 1024 * 1024

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

// Kolom VARCHAR yang tidak dibutuhkan tetap "dilewati" dengan benar (offset
// tetap maju pakai panjang yang sudah dibaca dari metadata di awal), tapi
// TIDAK PERNAH di-copy jadi Go string -- itu bagian termahal (alokasi +
// memcpy) yang dihindari. Kolom INT/FLOAT/BOOLEAN yang tidak dibutuhkan
// juga tidak diassign ke Value (menghindari boxing ke `any`), meski
// penghematannya jauh lebih kecil dibanding VARCHAR.
//
// Kolom yang di-skip tetap punya slot di Record (di index yang sama, supaya
// record[idx] tidak pernah out-of-range untuk kolom manapun), tapi isinya
// Value{} kosong. Pemanggil TIDAK BOLEH membaca nilai kolom yang tidak
// ditandai true di needed -- itu tanggung jawab pemanggil untuk hanya minta
// proyeksi yang benar-benar tidak ia butuhkan sama sekali (SELECT, WHERE,
// dan kolom PK).
func decodeRecord(columns []ColumnDef, data []byte, needed []bool) (record Record, flag uint8, nextOffset uint16, recordSize int, err error) {
	offset := 0

	isNeeded := func(idx int) bool {
		return needed == nil || (idx < len(needed) && needed[idx])
	}

	// -----------------------------------------
	// 1. Baca metadata panjang VARCHAR -- SELALU dibaca penuh untuk semua
	// kolom varchar (dibutuhkan atau tidak), karena posisi byte kolom-kolom
	// SESUDAHNYA bergantung pada angka ini. Ini murah (2 byte per kolom,
	// tanpa alokasi) -- yang mahal cuma materialize string-nya nanti.
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

		want := isNeeded(colIdx)

		if isNull {
			if want {
				record[colIdx] = Value{
					Type:  col.ValueType,
					Null:  true,
					Value: nil,
				}
			}
			continue
		}
		switch col.ValueType {
		case IntType:
			if offset+4 > len(data) {
				return nil, 0, 0, 0, ErrInvalidValue
			}

			if want {
				value := int32(binary.LittleEndian.Uint32(data[offset : offset+4]))
				record[colIdx] = Value{
					Type:  IntType,
					Value: value,
					Null:  false,
				}
			}
			offset += 4
		case FloatType:
			if offset+4 > len(data) {
				return nil, 0, 0, 0, ErrInvalidValue
			}
			if want {
				raw := binary.LittleEndian.Uint32(data[offset : offset+4])
				record[colIdx] = Value{
					Type:  FloatType,
					Value: math.Float32frombits(raw),
					Null:  false,
				}
			}
			offset += 4

		case BooleanType:
			if offset+1 > len(data) {
				return nil, 0, 0, 0, fmt.Errorf(
					"invalid record: boolean overflow column %s",
					col.Name,
				)
			}

			if want {
				record[colIdx] = Value{
					Type:  BooleanType,
					Value: data[offset] != 0,
					Null:  false,
				}
			}
			offset++
		case VarcharType:
			length := int(varcharLengths[colIdx])
			if offset+length > len(data) {
				return nil, 0, 0, 0, ErrInvalidValue
			}
			if want {
				// Satu-satunya baris yang benar-benar mahal (alokasi +
				// memcpy) di seluruh fungsi ini -- inilah yang dihindari
				// kalau kolom tidak dibutuhkan.
				record[colIdx] = Value{
					Type:  VarcharType,
					Value: string(data[offset : offset+length]),
					Null:  false,
				}
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

func readInternalCells(page *Page) (firstChild PageID, cells []InternalCell, err error) {
	head, err := DecodeIndexPageHeader(page)
	if err != nil {
		return firstChild, nil, err
	}

	if head.PageType != PageTypeInternal {
		return firstChild, nil, ErrCorruptTableFile
	}

	offset := IndexPageHeaderSize

	if offset+4 > PageSize {
		return 0, nil, ErrCorruptTableFile
	}

	firstChild = PageID(
		binary.LittleEndian.Uint32(
			page.Data[offset : offset+4],
		),
	)
	offset += 4

	cells = make([]InternalCell, 0, head.RecordCount)
	for i := 0; i < int(head.RecordCount); i++ {
		if offset+8 > PageSize {
			return 0, nil, ErrCorruptTableFile
		}

		sep := int32(binary.LittleEndian.Uint32(page.Data[offset : offset+4]))
		offset += 4
		child := PageID(binary.LittleEndian.Uint32(page.Data[offset : offset+4]))
		offset += 4
		cells = append(cells, InternalCell{
			SeparatorKey: sep,
			ChildPageID:  child,
		})
	}

	return firstChild, cells, nil
}

func rewriteInternal(page *Page, firstChild PageID, cells []InternalCell) error {
	header, err := DecodeIndexPageHeader(page)
	if err != nil {
		return err
	}
	if header.PageType != PageTypeInternal {
		return ErrCorruptTableFile
	}

	if firstChild == 0 {
		return ErrCorruptTableFile
	}
	requiredSize := int(IndexPageHeaderSize) + 4 + (len(cells) * 8)

	if requiredSize > int(PageSize) {
		return ErrInternalPageFull
	}

	page.Data = [PageSize]byte{}
	header.RecordCount = uint16(len(cells))
	header.FirstRecordOffset = 0
	header.FreeStart = uint16(requiredSize)
	header.PrevLeaf = InvalidPageID
	header.NextLeaf = InvalidPageID

	offset := IndexPageHeaderSize
	binary.LittleEndian.PutUint32(page.Data[offset:offset+4], uint32(firstChild))
	offset += 4

	for _, cell := range cells {
		// separator
		binary.LittleEndian.PutUint32(
			page.Data[offset:offset+4],
			uint32(cell.SeparatorKey),
		)

		offset += 4

		binary.LittleEndian.PutUint32(
			page.Data[offset:offset+4],
			uint32(cell.ChildPageID),
		)

		offset += 4
	}
	EncodeIndexPageHeader(page, header)
	return nil
}

func insertInternalCell(page *Page, separatorKey int32, childPageID PageID) error {
	firstChild, cells, err := readInternalCells(page)
	if err != nil {
		return err
	}

	for _, cell := range cells {
		if cell.SeparatorKey == separatorKey {
			return fmt.Errorf("duplicate internal separator: %d", separatorKey)
		}
	}

	cells = append(cells, InternalCell{
		SeparatorKey: separatorKey,
		ChildPageID:  childPageID,
	})

	sort.Slice(cells, func(i, j int) bool {
		return cells[i].SeparatorKey < cells[j].SeparatorKey
	})

	return rewriteInternal(page, firstChild, cells)
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

	if head.PageType != PageTypeLeaf {
		return nil, ErrCorruptTableFile
	}

	records := make([]LeafRecord, 0, head.RecordCount)
	currentOffset := head.FirstRecordOffset

	for currentOffset != 0 {
		if int(currentOffset) >= int(PageSize) {
			return nil, ErrCorruptTableFile
		}

		record, _, nextOffset, recordSize, err := decodeRecord(columns, page.Data[currentOffset:], nil)
		if err != nil {
			return nil, err
		}

		if pkColIdx >= len(record) {
			return nil, ErrCorruptTableFile
		}

		pkVal, ok := record[pkColIdx].Value.(int32)
		if !ok {
			return nil, ErrInvalidDataType
		}

		recordBytes := make([]byte, recordSize)
		copy(recordBytes, page.Data[currentOffset:int(currentOffset)+recordSize])

		records = append(records, LeafRecord{
			PK:   pkVal,
			Data: recordBytes,
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

	if header.PageType != PageTypeLeaf {
		return ErrCorruptTableFile
	}

	// Reset data page area
	page.Data = [PageSize]byte{}

	header.RecordCount = uint16(len(records))
	header.FirstRecordOffset = 0
	header.FreeStart = IndexPageHeaderSize

	if len(records) == 0 {
		EncodeIndexPageHeader(page, header)
		return nil
	}

	var prevOffset uint16 = 0
	currentFree := uint16(IndexPageHeaderSize)

	for i, rec := range records {
		var nextOffset uint16 = 0
		if i == 0 {
			header.FirstRecordOffset = currentFree
		} else {
			nextPos := int(prevOffset) + recordNextOffsetPosition(columns)
			binary.LittleEndian.PutUint16(page.Data[nextPos:nextPos+2], currentFree)
		}

		recordEnd := int(currentFree) + len(rec.Data)
		if recordEnd > int(PageSize) {
			return ErrValueOutOfRange
		}

		copy(page.Data[currentFree:recordEnd], rec.Data)

		// Set NextOffset field di dalam record menjadi 0 terlebih dahulu
		nextPos := int(currentFree) + recordNextOffsetPosition(columns)
		binary.LittleEndian.PutUint16(page.Data[nextPos:nextPos+2], nextOffset)

		prevOffset = currentFree
		currentFree = uint16(recordEnd)
	}

	header.FreeStart = currentFree
	EncodeIndexPageHeader(page, header)

	return nil
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

	if meta.Magic != [4]byte{'3', 'D', 'B', '1'} {
		return meta, ErrNotADatabaseFile
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

	binary.LittleEndian.PutUint32(page.Data[17:21], uint32(h.PrevLeaf))

	binary.LittleEndian.PutUint32(page.Data[21:25], uint32(h.NextLeaf))
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

	_, err = binary.Decode(page.Data[17:21], binary.LittleEndian, &h.PrevLeaf)
	if err != nil {
		return h, err
	}

	_, err = binary.Decode(page.Data[21:25], binary.LittleEndian, &h.NextLeaf)
	if err != nil {
		return h, err
	}
	return h, nil
}

func recordNextOffsetPosition(columns []ColumnDef) int {
	var varcharCount int
	var nullableCount int

	for _, col := range columns {
		if col.ValueType == VarcharType {
			varcharCount++
		}
		if col.Nullable {
			nullableCount++
		}
	}

	nullBitmapLen := (nullableCount + 7) / 8

	return (varcharCount * 2) + nullBitmapLen + 1
}

func updateChildrenParentID(
	pager *Pager,
	parentID PageID,
	firstChild PageID,
	cells []InternalCell,
) error {
	childIDs := make([]PageID, 0, len(cells)+1)
	childIDs = append(childIDs, firstChild)

	for _, cell := range cells {
		childIDs = append(childIDs, cell.ChildPageID)
	}

	for _, childID := range childIDs {
		childPage, err := pager.ReadPage(childID)
		if err != nil {
			return err
		}

		childHead, err := DecodeIndexPageHeader(childPage)
		if err != nil {
			return err
		}

		childHead.ParentID = parentID
		EncodeIndexPageHeader(childPage, childHead)

		if err := pager.WritePage(childPage); err != nil {
			return err
		}
	}

	return nil
}

func evaluateWhereClause(record Record, columns []ColumnDef, wc *WhereClause) (bool, error) {
	if wc == nil {
		return true, nil
	}

	result, err := evaluatePredicate(record, columns, wc)
	if err != nil {
		return false, err
	}

	for _, next := range wc.Criteria {
		nextResult, err := evaluateWhereClause(record, columns, next)
		if err != nil {
			return false, err
		}

		switch next.Logic {
		case AND:
			result = result && nextResult
		case OR:
			result = result || nextResult
		default:
			return false, fmt.Errorf("%w: logic operator tidak dikenal", ErrInvalidStatement)
		}
	}

	return result, nil
}

func evaluatePredicate(record Record, columns []ColumnDef, wc *WhereClause) (bool, error) {
	colIdx := -1
	var colDef ColumnDef
	for i, col := range columns {
		if col.Name == wc.Key {
			colIdx = i
			colDef = col
			break
		}
	}
	if colIdx == -1 {
		return false, fmt.Errorf("%w: kolom %s pada WHERE", ErrColumnNotFound, wc.Key)
	}

	rawVal, ok := wc.Val.(string)
	if !ok {
		return false, fmt.Errorf("%w: nilai WHERE tidak valid", ErrInvalidDataType)
	}

	wantValue, err := parseValue(rawVal, colDef.ValueType)
	if err != nil {
		return false, err
	}

	got := record[colIdx]

	if got.Null || wantValue == nil {
		switch wc.Op {
		case OpEq:
			return got.Null && wantValue == nil, nil
		case OpNeq:
			return !(got.Null && wantValue == nil), nil
		default:
			return false, fmt.Errorf("%w: operator selain = / <> tidak berlaku untuk NULL", ErrInvalidDataType)
		}
	}

	switch colDef.ValueType {
	case IntType:
		a, aok := got.Value.(int32)
		b, bok := wantValue.(int32)
		if !aok || !bok {
			return false, ErrInvalidDataType
		}
		return compareOrdered(int64(a), int64(b), wc.Op)

	case FloatType:
		a, aok := got.Value.(float32)
		b, bok := wantValue.(float32)
		if !aok || !bok {
			return false, ErrInvalidDataType
		}
		return compareOrdered(float64(a), float64(b), wc.Op)

	case VarcharType:
		a, aok := got.Value.(string)
		b, bok := wantValue.(string)
		if !aok || !bok {
			return false, ErrInvalidDataType
		}
		return compareOrdered(a, b, wc.Op)

	case BooleanType:
		a, aok := got.Value.(bool)
		b, bok := wantValue.(bool)
		if !aok || !bok {
			return false, ErrInvalidDataType
		}
		if wc.Op != OpEq && wc.Op != OpNeq {
			return false, fmt.Errorf("%w: operator selain = / <> tidak berlaku untuk BOOLEAN", ErrInvalidDataType)
		}
		if wc.Op == OpEq {
			return a == b, nil
		}
		return a != b, nil

	default:
		return false, ErrInvalidDataType
	}
}

func compareOrdered[T int64 | float64 | string](a, b T, op CompareOp) (bool, error) {
	switch op {
	case OpEq:
		return a == b, nil
	case OpNeq:
		return a != b, nil
	case OpGt:
		return a > b, nil
	case OpGte:
		return a >= b, nil
	case OpLt:
		return a < b, nil
	case OpLte:
		return a <= b, nil
	default:
		return false, fmt.Errorf("%w: operator tidak dikenal", ErrInvalidDataType)
	}
}
