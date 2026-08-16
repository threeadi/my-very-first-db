package main

import (
	"os"
	"path/filepath"
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
	assert.Equal(t, head.Level, uint16(2))
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
		Level:             2,
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
	catalog := NewCatalog() // sesuaikan dengan constructor-mu
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
	}

	for _, v := range values {
		err := executor.Insert(InsertStatement{
			DBName: "testdb",
			Table:  "users",
			Values: []string{v.id, v.name},
		})

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
