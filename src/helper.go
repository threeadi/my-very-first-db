package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
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
		if length > MAX_STRING_LENGTH {
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

		if length > MAX_STRING_LENGTH {
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

func readInternalCells(page *Page) (firstChild PageID, cells []InternalCell, err error) {
	head, err := DecodeIndexPageHeader(page)
	if err != nil {
		return firstChild, nil, err
	}

	if head.PageType != PageTypeInternal {
		return firstChild, nil, ErrCorruptTableFile
	}

	offset := IndexPageHeaderSize

	if offset+4 > int(PageSize) {
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
		if offset+8 > int(PageSize) {
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
	requiredSize := IndexPageHeaderSize + 4 + (len(cells) * 8)

	if requiredSize > int(PageSize) {
		return ErrInternalPageFull
	}

	page.Data = [PageSize]byte{}
	header.RecordCount = uint16(len(cells))
	header.FirstRecordOffset = 0
	header.FreeStart = uint16(requiredSize)

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

func updateChildrenParentID(
	pager *Pager,
	parentPageID PageID,
	firstChild PageID,
	cells []InternalCell,
) error {
	childIDs := []PageID{firstChild}
	for _, cell := range cells {
		childIDs = append(childIDs, cell.ChildPageID)
	}

	for _, childID := range childIDs {
		child, err := pager.ReadPage(childID)
		if err != nil {
			return err
		}

		header, err := DecodeIndexPageHeader(child)
		if err != nil {
			return err
		}

		header.ParentID = parentPageID
		EncodeIndexPageHeader(child, header)

		if err := pager.WritePage(child); err != nil {
			return err
		}
	}

	return nil
}

func validateChildrenParentID(
	pager *Pager,
	parentID PageID,
	firstChild PageID,
	cells []InternalCell,
) error {
	childIDs := []PageID{firstChild}
	for _, cell := range cells {
		childIDs = append(childIDs, cell.ChildPageID)
	}

	for _, childID := range childIDs {
		child, err := pager.ReadPage(childID)
		if err != nil {
			return err
		}

		header, err := DecodeIndexPageHeader(child)
		if err != nil {
			return err
		}

		if header.ParentID != parentID {
			return fmt.Errorf(
				"Salah parentID: child=%d expected=%d actual=%d type=%d",
				childID,
				parentID,
				header.ParentID,
				header.PageType,
			)
		}

	}
	return nil
}
