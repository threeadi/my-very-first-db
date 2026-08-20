package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
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

type LeafRecord struct {
	PK   int32
	Data []byte
}

type InternalCell struct {
	SeparatorKey int32
	ChildPageID  PageID
}

type Executor struct {
	config  *Config
	catalog *Catalog
	pagers  map[string]*Pager
}

func NewExecutor(config *Config, catalog *Catalog) *Executor {
	return &Executor{
		config:  config,
		catalog: catalog,
		pagers:  make(map[string]*Pager),
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

	filePath := filepath.Join(x.config.DataDirectory, fmt.Sprintf("/%s/%s.3tbl", stmt.DBName, stmt.Table))
	pager, err := CreatePager(filePath)
	if err != nil {
		return err
	}
	x.pagers[stmt.Table] = pager

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

	pager, exists := x.pagers[stmt.Table]
	if !exists {
		filePath := filepath.Join(x.config.DataDirectory, fmt.Sprintf("/%s/%s.3tbl", stmt.DBName, stmt.Table))
		pager, err = OpenPager(filePath)
		if err != nil {
			return err
		}
	}

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
		rootHead, err := DecodeIndexPageHeader(rootPage)
		if err != nil {
			return err
		}

		if rootHead.PageType == PageTypeLeaf {
			return splitRootLeaf(pager, page, columns, pkColIdx, newPK, parsedValues)
		}

		return splitLeaf(pager, page, columns, pkColIdx, newPK, parsedValues)
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

	pager, exists := x.pagers[stmt.Table]
	if !exists {
		filePath := filepath.Join(x.config.DataDirectory, fmt.Sprintf("/%s/%s.3tbl", stmt.DBName, stmt.Table))
		pager, err = OpenPager(filePath)
		if err != nil {
			return ResultSet{}, err
		}
	}

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

	records, err := scanTree(pager, rootPage, columns)
	if err != nil {
		return ResultSet{}, err
	}

	resultSet.Records = records

	return resultSet, nil
}

func scanTree(pager *Pager, page *Page, columns []ColumnDef) ([]Record, error) {
	head, err := DecodeIndexPageHeader(page)
	if err != nil {
		return nil, err
	}

	switch head.PageType {
	case PageTypeLeaf:
		return scanLeaf(page, columns)

	case PageTypeInternal:
		firstPageID, cells, err := readInternalCells(page)
		if err != nil {
			return nil, err
		}
		var childIDs []PageID = make([]PageID, 0, len(cells)+1)
		childIDs = append(childIDs, firstPageID)

		for _, cell := range cells {
			childIDs = append(childIDs, cell.ChildPageID)
		}

		var records []Record

		for _, id := range childIDs {
			child, err := pager.ReadPage(id)
			if err != nil {
				return nil, err
			}
			result, err := scanTree(pager, child, columns)
			if err != nil {
				return nil, err
			}
			records = append(records, result...)
		}

		return records, nil

	default:
		return nil, ErrCorruptTableFile
	}
}

func scanLeaf(page *Page, columns []ColumnDef) ([]Record, error) {
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

func splitInternalPage(
	pager *Pager,
	internal *Page,
	newSeparator int32,
	newChild PageID,
) error {
	firstPage, cells, err := readInternalCells(internal)
	if err != nil {
		return err
	}

	for _, cell := range cells {
		if cell.SeparatorKey == newSeparator {
			return fmt.Errorf("duplicate internal separator: %d", newSeparator)
		}
	}

	cells = append(cells, InternalCell{
		SeparatorKey: newSeparator,
		ChildPageID:  newChild,
	})

	sort.Slice(cells, func(i, j int) bool {
		return cells[i].SeparatorKey < cells[j].SeparatorKey
	})

	mid := len(cells) / 2
	promoted := cells[mid]

	leftFirstChild := firstPage
	leftCells := cells[:mid]

	rightFirstChild := promoted.ChildPageID
	rightCells := cells[mid+1:]
	if err = rewriteInternal(internal, leftFirstChild, leftCells); err != nil {
		return err
	}

	rightInternal, err := pager.AllocatePage()

	if err != nil {
		return err
	}
	leftHead, err := DecodeIndexPageHeader(internal)
	if err != nil {
		return err
	}

	rightHeader := IndexPageHeader{
		PageType: PageTypeInternal,
		PageID:   rightInternal.ID,
		ParentID: leftHead.ParentID,
		Level:    leftHead.Level,
	}

	EncodeIndexPageHeader(rightInternal, rightHeader)

	if err = rewriteInternal(rightInternal, rightFirstChild, rightCells); err != nil {
		return err
	}

	rightHeader, err = DecodeIndexPageHeader(rightInternal)
	if err != nil {
		return err
	}

	if err = pager.WritePage(rightInternal); err != nil {
		return err
	}

	if err := updateChildrenParentID(
		pager,
		rightInternal.ID,
		rightFirstChild,
		rightCells,
	); err != nil {
		return err
	}

	if leftHead.ParentID == InvalidPageID {
		root, err := pager.AllocatePage()
		if err != nil {
			return err
		}

		EncodeIndexPageHeader(root, IndexPageHeader{
			PageType: PageTypeInternal,
			PageID:   root.ID,
			ParentID: InvalidPageID,
			Level:    rightHeader.Level + 1,
		})

		err = rewriteInternal(root, internal.ID, []InternalCell{
			{
				SeparatorKey: promoted.SeparatorKey,
				ChildPageID:  rightInternal.ID,
			},
		})
		if err != nil {
			return err
		}

		if err = pager.WritePage(root); err != nil {
			return err
		}

		fmt.Println("success creating new root. ID", root.ID)
		leftHead.ParentID = root.ID
		EncodeIndexPageHeader(internal, leftHead)
		rightHeader.ParentID = root.ID
		EncodeIndexPageHeader(rightInternal, rightHeader)

		if err = pager.WritePage(internal); err != nil {
			return err
		}

		if err = pager.WritePage(rightInternal); err != nil {
			return err
		}

		meta, err := pager.ReadPage(PageID(0))
		if err != nil {
			return err
		}
		metaPage, err := DecodeMetaPage(meta)
		if err != nil {
			return err
		}
		metaPage.RootPageID = root.ID
		return pager.WritePage(EncodeMetaPage(metaPage))
	}

	root, err := pager.ReadPage(leftHead.ParentID)
	if err != nil {
		return err
	}

	err = insertInternalCell(root, promoted.SeparatorKey, rightInternal.ID)
	if err != nil {
		if !errors.Is(err, ErrInternalPageFull) {
			return err
		}

		fmt.Println("ADD new separator FAILED due to root is full. ROOT ID:", root.ID)
		err = splitInternalPage(pager, root, promoted.SeparatorKey, rightInternal.ID)
		if err != nil {
			return err
		}
	} else {
		if err := pager.WritePage(root); err != nil {
			return err
		}
	}

	if err = pager.WritePage(internal); err != nil {
		return err
	}

	return nil

}

func splitLeaf(
	pager *Pager,
	leafPage *Page,
	cols []ColumnDef,
	pkColIdx int,
	newPK int32,
	parsedValues []ParsedValue,
) error {
	leafHeader, err := DecodeIndexPageHeader(leafPage)
	if err != nil {
		return err
	}

	if leafHeader.PageType != PageTypeLeaf {
		return ErrCorruptTableFile
	}

	if leafHeader.ParentID == InvalidPageID {
		return errors.New("splitLeaf tidak untuk root leaf")
	}

	records, err := readLeafRecords(leafPage, cols, pkColIdx)
	if err != nil {
		return err
	}

	newRecordData, err := encodeRecord(cols, parsedValues, 0)
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

	mid := len(records) / 2

	leftRecords := records[:mid]
	rightRecords := records[mid:]

	if err = rewriteLeaf(leafPage, leftRecords, cols); err != nil {
		return err
	}

	if err = pager.WritePage(leafPage); err != nil {
		return err
	}

	rightPage, err := pager.AllocatePage()
	if err != nil {
		return err
	}

	EncodeIndexPageHeader(rightPage, IndexPageHeader{
		PageType:          PageTypeLeaf,
		PageID:            rightPage.ID,
		ParentID:          leafHeader.ParentID,
		Level:             0,
		RecordCount:       0,
		FirstRecordOffset: 0,
		FreeStart:         IndexPageHeaderSize,
	})

	if err = rewriteLeaf(rightPage, rightRecords, cols); err != nil {
		return err
	}

	if err := pager.WritePage(rightPage); err != nil {
		return err
	}

	separatorKey := rightRecords[0].PK
	parentPage, err := pager.ReadPage(leafHeader.ParentID)
	if err != nil {
		return err
	}

	err = insertInternalCell(parentPage, separatorKey, rightPage.ID)
	if err != nil {
		if !errors.Is(err, ErrInternalPageFull) {
			return err
		}

		err = splitInternalPage(pager, parentPage, separatorKey, rightPage.ID)
		if err != nil {
			return err
		}

	} else {
		if err := pager.WritePage(parentPage); err != nil {
			return err
		}
	}

	return nil
}

func splitRootLeaf(
	pager *Pager,
	rootPage *Page,
	columns []ColumnDef,
	pkColIdx int,
	newPK int32,
	parsedValues []ParsedValue,
) error {
	records, err := readLeafRecords(rootPage, columns, pkColIdx)
	if err != nil {
		return err
	}

	newRecordData, err := encodeRecord(columns, parsedValues, 0)
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

	mid := len(records) / 2

	leftRecords := records[:mid]
	rightRecords := records[mid:]

	if len(leftRecords) == 0 || len(rightRecords) == 0 {
		return errors.New("invalid leaf split")
	}

	// =====================================================
	// PAGE 1 = LEFT LEAF
	// =====================================================

	if err := rewriteLeaf(rootPage, leftRecords, columns); err != nil {
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

	var childID PageID

	firstChild, cells, err := readInternalCells(parent)
	if err != nil {
		return nil, err
	}

	childID = firstChild
	for _, cell := range cells {
		if newPK < cell.SeparatorKey {
			break
		}
		childID = cell.ChildPageID
	}

	if childID <= 0 {
		return nil, ErrCorruptTableFile
	}

	child, err := pager.ReadPage(childID)
	if err != nil {
		return nil, err
	}

	return x.targetPage(pager, child, newPK)
}

func (x *Executor) Close() error {
	for _, pager := range x.pagers {
		err := pager.Close()
		if err != nil {
			fmt.Println("failed to close pager", err)
			return err
		}
	}
	return nil
}
