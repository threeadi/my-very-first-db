package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-playground/assert/v2"
)

func TestCreateEmptyTree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.3tbl")
	err := createEmptyTree(path)
	if err != nil {
		t.Fatal(err)
	}
	pager, err := OpenPager(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err = pager.Close()
		if err == nil {
			os.Remove(path)
		}
	}()

	page, err := pager.ReadPage(PageID(0))
	if err != nil {
		t.Fatal(err)
	}
	metaPage, err := DecodeMetaPage(page)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, metaPage.RootPageID, PageID(1))
	assert.Equal(t, metaPage.NextPageID, PageID(2))
	assert.Equal(t, metaPage.PageSize, PageSize)

	rootPage, err := pager.ReadPage(PageID(1))
	if err != nil {
		t.Fatal(err)
	}

	head, err := DecodeIndexPageHeader(rootPage)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, head.PageID, rootPage.ID)
	assert.Equal(t, head.PageType, PageTypeLeaf)
	assert.Equal(t, head.ParentID, InvalidPageID)
	assert.Equal(t, head.Level, uint16(0))
	assert.Equal(t, head.FirstRecordOffset, uint16(0))           // means this record is latest
	assert.Equal(t, head.FreeStart, uint16(IndexPageHeaderSize)) // means record start at 17

}

func createEmptyTree(path string) error {
	pager, err := CreatePager(path)
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

	rootPage, err := pager.AllocatePage()
	if err != nil {
		return err
	}

	h := IndexPageHeader{
		PageType:          PageTypeLeaf,
		PageID:            rootPage.ID,
		ParentID:          InvalidPageID,
		Level:             0,
		RecordCount:       0,
		FirstRecordOffset: 0,
		FreeStart:         IndexPageHeaderSize,
	}

	EncodeIndexPageHeader(rootPage, h)
	err = pager.WritePage(rootPage)
	if err != nil {
		return err
	}

	return nil
}

func TestInsertSingleLeafOrdered(t *testing.T) {
	tempDir := t.TempDir()
	defer os.RemoveAll(tempDir)
	config := &Config{
		DataDirectory: tempDir + string(os.PathSeparator),
		CatalogPath:   filepath.Join(tempDir, "catalog.json"),
	}
	catalog := NewCatalog() 
	executor := NewExecutor(config, catalog)
	err := executor.CreateDatabase(CreateDatabaseStatement{
		DBName: "testdb",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = executor.CreateTable(CreateTableStatement{
		DBName: "testdb",
		Table:  "users",
		Columns: []ColumnDef{
			{
				Name:      "id",
				ValueType: IntType,
				Primary:   true,
				Nullable:  false,
			},
			{
				Name:      "name",
				ValueType: VarcharType,
				Nullable:  false,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// insert: 10, 20, 5, 15
	values := []struct {
		id   string
		name string
	}{
		{"10", "sepuluh"},
		{"20", "dua puluh"},
		{"5", "lima"},
		{"15", "lima belas"},
		{"100", strings.Repeat("a", 4096)},
	}

	for _, v := range values {
		err := executor.Insert(InsertStatement{
			DBName: "testdb",
			Table:  "users",
			Values: []string{v.id, v.name},
		})

		if err != nil && v.id == "100" {
			assert.IsEqual(ErrValueOutOfRange, err)
			continue
		}

		if err != nil {
			t.Fatal(err)
		}
	}

	// scan leaf
	result, err := executor.Select(SelectStatement{
		DBName:  "testdb",
		Table:   "users",
		Columns: []string{"*"},
	})
	if err != nil {
		t.Fatal(err)
	}

	expected := []int32{5, 10, 15, 20}
	assert.Equal(t, len(expected), len(result.Records))
	for i, expectedPK := range expected {
		got := result.Records[i][0].Value
		assert.Equal(t, expectedPK, got)
	}
}

// TEST SPLIT ROOT LEAF
// INSERT:
// 10
// 20
// 30
// 40
// 50

// record 5 menyebabkan split

//	                    Meta
//	                RootPageID=3
//	                     │
//	                     ▼
//	               ┌────────────┐
//	               │   Page 3   │
//	               │  INTERNAL  │
//	               │            │
//	               │ sep = 30   │
//	               └─────┬──────┘
//	                     │
//	             ┌───────┴───────┐
//	             ▼               ▼
//			┌─────────┐      ┌─────────┐
//			│ Page 1  │      │ Page 2  │
//			│  LEAF   │      │  LEAF   │
//			│Parent=3 │      │Parent=3 │
//			├─────────┤      ├─────────┤
//			│ 10      │      │ 30      │
//			│ ↓       │      │ ↓       │
//			│ 20      │      │ 40      │
//			│ ↓       │      │ ↓       │
//			│ END     │      │ 50      │
//			│         │      │ ↓       │
//			│         │      │ END     │
//			└─────────┘      └─────────┘
func TestInsertSplitRootLeaf(t *testing.T) {
	tempDir := t.TempDir()
	config := &Config{
		DataDirectory: tempDir + string(os.PathSeparator),
		CatalogPath:   filepath.Join(tempDir, "catalog.json"),
	}

	catalog := NewCatalog()
	executor := NewExecutor(config, catalog)
	err := executor.CreateDatabase(CreateDatabaseStatement{
		DBName: "testdb",
	})
	if err != nil {
		t.Fatal(err)
	}

	err = executor.CreateTable(CreateTableStatement{
		DBName: "testdb",
		Table:  "users",
		Columns: []ColumnDef{
			{
				Name:      "id",
				ValueType: IntType,
				Primary:   true,
				Nullable:  false,
			},
			{
				Name:      "name",
				ValueType: VarcharType,
				Nullable:  false,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	largeValue := strings.Repeat("a", 1000)
	// 4 record masih muat.
	// Record ke-5 akan membuat root leaf split.
	ids := []string{
		"10",
		"20",
		"30",
		"40",
		"50",
	}
	for _, id := range ids {
		err := executor.Insert(InsertStatement{
			DBName: "testdb",
			Table:  "users",
			Values: []string{
				id,
				largeValue,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	path := filepath.Join(
		config.DataDirectory,
		"testdb",
		"users.3tbl",
	)

	pager, err := OpenPager(path)
	if err != nil {
		t.Fatal(err)
	}
	defer pager.Close()

	metaPage, err := pager.ReadPage(PageID(0))
	if err != nil {
		t.Fatal(err)
	}

	meta, err := DecodeMetaPage(metaPage)
	if err != nil {
		t.Fatal(err)
	}

	// Setelah split:
	//
	// Page 1 = left leaf
	// Page 2 = right leaf
	// Page 3 = internal root
	//
	assert.Equal(t, meta.RootPageID, PageID(3))

	rootPage, err := pager.ReadPage(PageID(meta.RootPageID))
	if err != nil {
		t.Fatal(err)
	}

	rootHeader, err := DecodeIndexPageHeader(rootPage)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, rootHeader.PageType, PageTypeInternal)
	assert.Equal(t, rootHeader.PageID, meta.RootPageID)
	assert.Equal(t, rootHeader.Level, uint16(1))
	assert.Equal(t, rootHeader.ParentID, InvalidPageID)
	assert.Equal(t, rootHeader.RecordCount, uint16(1))

	assert.Equal(
		t,
		rootHeader.FreeStart,
		uint16(IndexPageHeaderSize+12),
	)
	offset := IndexPageHeaderSize
	leftPageID := PageID(binary.LittleEndian.Uint32(rootPage.Data[offset : offset+4]))
	offset += 4
	separator := int32(binary.LittleEndian.Uint32(rootPage.Data[offset : offset+4]))
	offset += 4

	rightPageID := PageID(binary.LittleEndian.Uint32(rootPage.Data[offset : offset+4]))
	assert.Equal(t, leftPageID, PageID(1))
	assert.Equal(t, rightPageID, PageID(2))

	assert.Equal(t, separator, int32(30))
	leftPage, err := pager.ReadPage(leftPageID)
	if err != nil {
		t.Fatal(err)
	}
	leftHeader, err := DecodeIndexPageHeader(leftPage)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, leftHeader.PageType, PageTypeLeaf)
	assert.Equal(t, leftHeader.ParentID, meta.RootPageID)
	assert.Equal(t, leftHeader.Level, uint16(0))
	assert.Equal(t, leftHeader.RecordCount, uint16(2))

	rightPage, err := pager.ReadPage(rightPageID)
	if err != nil {
		t.Fatal(err)
	}
	rightHeader, err := DecodeIndexPageHeader(rightPage)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, rightHeader.PageType, PageTypeLeaf)
	assert.Equal(t, rightHeader.ParentID, meta.RootPageID)
	assert.Equal(t, rightHeader.Level, uint16(0))
	assert.Equal(t, rightHeader.RecordCount, uint16(3))

	// leaf
	cols, err := catalog.GetTableColumns("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}

	leftRecords, err := readLeafRecords(
		leftPage,
		cols,
		0, // PK column = id
	)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, len(leftRecords), 2)
	assert.Equal(t, leftRecords[0].PK, int32(10))
	assert.Equal(t, leftRecords[1].PK, int32(20))

	rightRecords, err := readLeafRecords(
		rightPage,
		cols,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, len(rightRecords), 3)
	assert.Equal(t, rightRecords[0].PK, int32(30))
	assert.Equal(t, rightRecords[1].PK, int32(40))
	assert.Equal(t, rightRecords[2].PK, int32(50))
}
