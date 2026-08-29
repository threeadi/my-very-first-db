package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
	defer pager.Close()

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

func TestInsert(t *testing.T) {
	tempDir := t.TempDir()
	t.Log("tempDir: ", tempDir)

	config := &Config{
		DataDirectory: tempDir + string(os.PathSeparator),
		CatalogPath: filepath.Join(
			tempDir,
			"catalog.json",
		),
	}

	catalog := NewCatalog()
	executor := NewExecutor(config, catalog)
	defer func() {
		executor.Close()
	}()

	err := executor.CreateDatabase(
		CreateDatabaseStatement{
			DBName: "testdb",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	err = executor.CreateTable(
		CreateTableStatement{
			DBName: "testdb",
			Table:  "users",
			Columns: []ColumnDef{
				{
					Name:      "id",
					ValueType: IntType,
					Primary:   true,
				},
				{
					Name:      "name",
					ValueType: VarcharType,
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	err = executor.Insert(
		InsertStatement{
			DBName: "testdb",
			Table:  "users",
			Values: []string{
				"1",
				"value",
			},
		},
	)

	if err != nil {
		t.Fatalf("insert 1 failed: %v", err)
	}

	err = executor.Insert(
		InsertStatement{
			DBName: "testdb",
			Table:  "users",
			Values: []string{
				"1.123",
				"false",
			},
		},
	)

	if !errors.Is(err, ErrInvalidDataType) {
		t.Fatalf("expected ErrInvalidDataType, got: %v", err)
	}
}

func TestInsertSingleLeafOrdered(t *testing.T) {
	tempDir := t.TempDir()
	config := &Config{
		DataDirectory: tempDir + string(os.PathSeparator),
		CatalogPath:   filepath.Join(tempDir, "catalog.json"),
	}
	catalog := NewCatalog()
	executor := NewExecutor(config, catalog)
	defer func() {
		executor.Close()
	}()
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

	largeValue := createLargeString()
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

	path := filepath.Join(config.DataDirectory, "testdb", "users.3tbl")

	pager, err := OpenPager(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		pager.Close()
		executor.Close()
	}()

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

	selectCols := []string{"*"}
	result, err := executor.Select(SelectStatement{
		DBName:  "testdb",
		Table:   "users",
		Columns: selectCols,
	})
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, len(ids), len(result.Records))
}

func TestInsertSplitLeafSameRoot(t *testing.T) {
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

	largeValue := createLargeString()

	// 9 record dalam 4 page berbeda
	ids := []string{
		"10", // page 1
		"20", //page 1
		"30", // |<- separator  (page 2)
		"40", // | page 2
		"50", //  <- separator (page 4)
		"60", // | page 4
		"70", // | <- separator (page 5)
		"80", // | page 5
		"90", // page 5
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

	path := filepath.Join(config.DataDirectory, "testdb", "users.3tbl")

	pager, err := OpenPager(path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		pager.Close()
		executor.Close()
	}()

	metaPage, err := pager.ReadPage(PageID(0))
	if err != nil {
		t.Fatal(err)
	}

	meta, err := DecodeMetaPage(metaPage)
	if err != nil {
		t.Fatal(err)
	}
	rootPage, err := pager.ReadPage(PageID(meta.RootPageID))
	if err != nil {
		t.Fatal(err)
	}

	rootHeader, err := DecodeIndexPageHeader(rootPage)
	if err != nil {
		t.Fatal(err)
	}

	cells := make([]InternalCell, 0, 3)
	cells = append(cells, InternalCell{
		SeparatorKey: 30,
		ChildPageID:  PageID(2),
	})
	cells = append(cells, InternalCell{
		SeparatorKey: 50,
		ChildPageID:  PageID(4),
	})
	cells = append(cells, InternalCell{
		SeparatorKey: 70,
		ChildPageID:  PageID(5),
	})
	assert.Equal(t, rootHeader.RecordCount, uint16(3))

	assert.Equal(
		t,
		rootHeader.FreeStart,
		uint16(IndexPageHeaderSize+28),
	)

	resFirstChildId, resultCells, err := readInternalCells(rootPage)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, PageID(1), resFirstChildId)
	for i, cell := range resultCells {
		assert.Equal(t, cells[i].SeparatorKey, cell.SeparatorKey)
		assert.Equal(t, cells[i].ChildPageID, cell.ChildPageID)
	}

	// test select
	res, err := executor.Select(SelectStatement{
		DBName:  "testdb",
		Table:   "users",
		Columns: []string{"*"},
	})
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, len(res.Records), len(ids))
}

func TestSplitRootInternal(t *testing.T) {
	tempDir := t.TempDir()

	config := &Config{
		DataDirectory: tempDir + string(os.PathSeparator),
		CatalogPath: filepath.Join(
			tempDir,
			"catalog.json",
		),
	}

	catalog := NewCatalog()
	executor := NewExecutor(config, catalog)

	err := executor.CreateDatabase(
		CreateDatabaseStatement{
			DBName: "testdb",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	err = executor.CreateTable(
		CreateTableStatement{
			DBName: "testdb",
			Table:  "users",
			Columns: []ColumnDef{
				{
					Name:      "id",
					ValueType: IntType,
					Primary:   true,
				},
				{
					Name:      "name",
					ValueType: VarcharType,
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	largeValue := createLargeString()

	// 2045 supaya root overflow
	for i := 1; i <= 2045; i++ {
		err := executor.Insert(
			InsertStatement{
				DBName: "testdb",
				Table:  "users",
				Values: []string{
					strconv.Itoa(i),
					largeValue,
				},
			},
		)

		if err != nil {
			t.Fatalf("insert %d failed: %v", i, err)
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

	defer func() {
		pager.Close()
		executor.Close()
	}()

	metaPage, err := pager.ReadPage(0)
	if err != nil {
		t.Fatal(err)
	}

	meta, err := DecodeMetaPage(metaPage)
	if err != nil {
		t.Fatal(err)
	}

	rootPage, err := pager.ReadPage(meta.RootPageID)
	if err != nil {
		t.Fatal(err)
	}

	rootHeader, err := DecodeIndexPageHeader(rootPage)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, PageTypeInternal, rootHeader.PageType)

	assert.Equal(t, uint16(2), rootHeader.Level)
}

func TestMultiLevelTreeInsertAndSelect(t *testing.T) {
	tempDir := t.TempDir()
	t.Log("tempDir: ", tempDir)

	config := &Config{
		DataDirectory: tempDir + string(os.PathSeparator),
		CatalogPath: filepath.Join(
			tempDir,
			"catalog.json",
		),
	}

	catalog := NewCatalog()
	executor := NewExecutor(config, catalog)
	defer func() {
		executor.Close()
	}()

	err := executor.CreateDatabase(
		CreateDatabaseStatement{
			DBName: "testdb",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	err = executor.CreateTable(
		CreateTableStatement{
			DBName: "testdb",
			Table:  "users",
			Columns: []ColumnDef{
				{
					Name:      "id",
					ValueType: IntType,
					Primary:   true,
				},
				{
					Name:      "name",
					ValueType: VarcharType,
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	largeValue := createLargeString()

	recordsTotal := 10_000
	decrementRecords := recordsTotal
	for i := 1; i <= recordsTotal/2; i++ {
		err := executor.Insert(
			InsertStatement{
				DBName: "testdb",
				Table:  "users",
				Values: []string{
					strconv.Itoa(i),
					largeValue,
				},
			},
		)

		if err != nil {
			t.Fatalf("insert %d failed: %v", i, err)
		}

		err = executor.Insert(
			InsertStatement{
				DBName: "testdb",
				Table:  "users",
				Values: []string{
					strconv.Itoa(decrementRecords),
					largeValue,
				},
			},
		)

		if err != nil {
			t.Fatalf("insert %d failed: %v", i, err)
		}
		decrementRecords--
		if decrementRecords <= recordsTotal/2 {
			break
		}
	}
	cols, err := catalog.GetTableColumns("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}

	pager, err := OpenPager(filepath.Join(config.DataDirectory, "testdb", "users.3tbl"))
	if err != nil {
		t.Fatal(err)
	}
	defer pager.Close()

	if err = ValidateTree(pager, cols, 0); err != nil {
		t.Fatal(err)
	}

	t.Log("the tree is valid")
	res, err := executor.Select(SelectStatement{
		DBName:  "testdb",
		Table:   "users",
		Columns: []string{"*"},
	})
	if err != nil {
		t.Fatal(err)
	}

	found := make(map[int32]bool, len(res.Records))
	for _, rec := range res.Records {
		pk, _ := rec[0].Value.(int32)
		found[pk] = true
	}

	var missing []int32
	for i := int32(1); i <= int32(recordsTotal); i++ {
		if !found[i] {
			missing = append(missing, i)
		}
	}

	t.Log("total missing:", len(missing))
	if len(missing) > 0 {
		t.Log("first missing:", missing[0], "last missing:", missing[len(missing)-1])
		t.Log("sample:", missing[:min(20, len(missing))])
	}

	assert.Equal(t, recordsTotal, len(res.Records))
}

func TestRestartInsertAndSelect(t *testing.T) {
	tempDir := t.TempDir()
	t.Log("tempDir: ", tempDir)

	config := &Config{
		DataDirectory: tempDir + string(os.PathSeparator),
		CatalogPath: filepath.Join(
			tempDir,
			"catalog.json",
		),
	}

	catalog, err := LoadCatalog(config.CatalogPath)
	if err != nil {
		t.Fatal(err)
	}

	executor := NewExecutor(config, catalog)
	err = executor.CreateDatabase(
		CreateDatabaseStatement{
			DBName: "testdb",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	err = executor.CreateTable(
		CreateTableStatement{
			DBName: "testdb",
			Table:  "users",
			Columns: []ColumnDef{
				{
					Name:      "id",
					ValueType: IntType,
					Primary:   true,
				},
				{
					Name:      "name",
					ValueType: VarcharType,
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	largeValue := createLargeString()

	recordsTotal := 1_000
	for i := 1; i <= recordsTotal; i++ {
		err := executor.Insert(
			InsertStatement{
				DBName: "testdb",
				Table:  "users",
				Values: []string{
					strconv.Itoa(i),
					largeValue,
				},
			},
		)

		if err != nil {
			t.Fatalf("insert %d failed: %v", i, err)
		}
	}

	err = executor.Close()
	if err != nil {
		t.Fatal(err)
	}
	catalog2, err := LoadCatalog(config.CatalogPath)
	if err != nil {
		t.Fatal(err)
	}

	// build new one
	exec2 := NewExecutor(config, catalog2)
	defer exec2.Close()

	res, err := exec2.Select(SelectStatement{
		DBName:  "testdb",
		Table:   "users",
		Columns: []string{"*"},
	})
	if err != nil {
		t.Fatal(err)
	}

	found := make(map[int32]bool, len(res.Records))
	for _, rec := range res.Records {
		pk, _ := rec[0].Value.(int32)
		found[pk] = true
	}

	assert.Equal(t, recordsTotal, len(res.Records))

	assert.Equal(t, recordsTotal, len(res.Records))
	cols, err := catalog.GetTableColumns("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	pager, err := OpenPager(filepath.Join(config.DataDirectory, "testdb", "users.3tbl"))
	if err != nil {
		t.Fatal(err)
	}
	defer pager.Close()

	if err = ValidateTree(pager, cols, 0); err != nil {
		t.Fatal(err)
	}
	t.Log("the tree is valid")
}

func TestFuzzyInsertRandomOrder(t *testing.T) {
	sizes := []int{50, 500, 2500}

	for _, n := range sizes {
		n := n
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			seed := time.Now().UnixNano()
			t.Logf("seed: %d (n=%d)", seed, n)
			rng := rand.New(rand.NewSource(seed))

			tempDir := t.TempDir()
			config := &Config{
				DataDirectory: tempDir + string(os.PathSeparator),
				CatalogPath:   filepath.Join(tempDir, "catalog.json"),
			}

			catalog := NewCatalog()
			executor := NewExecutor(config, catalog)
			defer executor.Close()

			if err := executor.CreateDatabase(CreateDatabaseStatement{
				DBName: "testdb",
			}); err != nil {
				t.Fatal(err)
			}

			if err := executor.CreateTable(CreateTableStatement{
				DBName: "testdb",
				Table:  "users",
				Columns: []ColumnDef{
					{Name: "id", ValueType: IntType, Primary: true, Nullable: false},
					{Name: "name", ValueType: VarcharType, Nullable: false},
				},
			}); err != nil {
				t.Fatal(err)
			}

			// PK 1..n, lalu diacak urutannya sebelum di-insert.
			ids := make([]int, n)
			for i := range ids {
				ids[i] = i + 1
			}
			rng.Shuffle(len(ids), func(i, j int) {
				ids[i], ids[j] = ids[j], ids[i]
			})

			largeValue := createLargeString()
			for _, id := range ids {
				err := executor.Insert(InsertStatement{
					DBName: "testdb",
					Table:  "users",
					Values: []string{
						strconv.Itoa(id),
						largeValue,
					},
				})
				if err != nil {
					t.Fatalf("insert id=%d gagal (seed=%d): %v", id, seed, err)
				}
			}

			cols, err := catalog.GetTableColumns("testdb", "users")
			if err != nil {
				t.Fatal(err)
			}

			pager, err := OpenPager(filepath.Join(config.DataDirectory, "testdb", "users.3tbl"))
			if err != nil {
				t.Fatal(err)
			}
			defer pager.Close()

			if err := ValidateTree(pager, cols, 0); err != nil {
				t.Fatalf("tree tidak valid setelah fuzzy insert (seed=%d): %v", seed, err)
			}

			res, err := executor.Select(SelectStatement{
				DBName:  "testdb",
				Table:   "users",
				Columns: []string{"*"},
			})
			if err != nil {
				t.Fatal(err)
			}

			assert.Equal(t, n, len(res.Records))

			// Inti test: walau insert diacak total, hasil scan HARUS ascending
			// 1..n tanpa lubang maupun duplikat.
			seen := make(map[int32]bool, n)
			for i, rec := range res.Records {
				pk, ok := rec[0].Value.(int32)
				if !ok {
					t.Fatalf("record %d: PK value bukan int32: %v", i, rec[0].Value)
				}

				wantPK := int32(i + 1)
				if pk != wantPK {
					t.Fatalf(
						"urutan hasil scan salah pada index %d (seed=%d): got PK=%d, want PK=%d",
						i, seed, pk, wantPK,
					)
				}

				if seen[pk] {
					t.Fatalf("duplicate PK %d ditemukan di hasil scan (seed=%d)", pk, seed)
				}
				seen[pk] = true
			}

			if len(seen) != n {
				t.Fatalf("jumlah PK unik di hasil scan = %d, want %d (seed=%d)", len(seen), n, seed)
			}
		})
	}
}

// TestFuzzyInsertRandomOrderWithDuplicates sama seperti di atas, tapi setiap
// PK dicoba di-insert dua kali dan seluruh urutan percobaan (bukan cuma
// urutan PK unik-nya) diacak total. Ini memastikan:
//   - Insert pertama untuk suatu PK selalu sukses, terlepas kapan giliran
//     PK itu muncul di urutan acak.
//   - Insert kedua untuk PK yang sama selalu ditolak duplicate, bahkan kalau
//     percobaan kedua itu "menyelip" jauh sebelum/sesudah PK lain di-insert.
//   - Tree tetap valid dan hasil scan tetap ascending sekalipun sebagian
//     besar operasi insert di tengah jalan gagal (bukan sukses semua).
func TestFuzzyInsertRandomOrderWithDuplicates(t *testing.T) {
	const n = 300

	seed := time.Now().UnixNano()
	t.Logf("seed: %d", seed)
	rng := rand.New(rand.NewSource(seed))

	tempDir := t.TempDir()
	config := &Config{
		DataDirectory: tempDir + string(os.PathSeparator),
		CatalogPath:   filepath.Join(tempDir, "catalog.json"),
	}

	catalog := NewCatalog()
	executor := NewExecutor(config, catalog)
	defer executor.Close()

	if err := executor.CreateDatabase(CreateDatabaseStatement{
		DBName: "testdb",
	}); err != nil {
		t.Fatal(err)
	}

	if err := executor.CreateTable(CreateTableStatement{
		DBName: "testdb",
		Table:  "users",
		Columns: []ColumnDef{
			{Name: "id", ValueType: IntType, Primary: true, Nullable: false},
			{Name: "name", ValueType: VarcharType, Nullable: false},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Setiap id 1..n muncul dua kali di daftar percobaan, lalu diacak total.
	attempts := make([]int, 0, n*2)
	for i := 1; i <= n; i++ {
		attempts = append(attempts, i, i)
	}
	rng.Shuffle(len(attempts), func(i, j int) {
		attempts[i], attempts[j] = attempts[j], attempts[i]
	})

	largeValue := createLargeString()
	inserted := make(map[int]bool, n)

	for _, id := range attempts {
		err := executor.Insert(InsertStatement{
			DBName: "testdb",
			Table:  "users",
			Values: []string{
				strconv.Itoa(id),
				largeValue,
			},
		})

		if !inserted[id] {
			if err != nil {
				t.Fatalf("percobaan pertama insert id=%d gagal (seed=%d): %v", id, seed, err)
			}
			inserted[id] = true
			continue
		}

		if err == nil {
			t.Fatalf("percobaan kedua insert id=%d seharusnya ditolak duplicate PK (seed=%d)", id, seed)
		}
	}

	cols, err := catalog.GetTableColumns("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}

	pager, err := OpenPager(filepath.Join(config.DataDirectory, "testdb", "users.3tbl"))
	if err != nil {
		t.Fatal(err)
	}
	defer pager.Close()

	if err := ValidateTree(pager, cols, 0); err != nil {
		t.Fatalf("tree tidak valid setelah fuzzy insert+duplicate (seed=%d): %v", seed, err)
	}

	res, err := executor.Select(SelectStatement{
		DBName:  "testdb",
		Table:   "users",
		Columns: []string{"*"},
	})
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, n, len(res.Records))

	for i, rec := range res.Records {
		pk, ok := rec[0].Value.(int32)
		if !ok {
			t.Fatalf("record %d: PK bukan int32", i)
		}
		wantPK := int32(i + 1)
		if pk != wantPK {
			t.Fatalf("urutan salah di index %d (seed=%d): got %d, want %d", i, seed, pk, wantPK)
		}
	}
}

func createLargeString() string {
	return strings.Repeat("a", 1000)
}

func ValidateTree(pager *Pager, columns []ColumnDef, pkColumn int) error {
	metaRaw, err := pager.ReadPage(PageID(0))
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

	root, err := DecodeIndexPageHeader(rootPage)
	if err != nil {
		return err
	}

	if root.ParentID != InvalidPageID {
		return fmt.Errorf("root page %d has parent %d",
			root.PageID,
			root.ParentID,
		)
	}

	visited := make(map[PageID]bool)
	var leafLevels []uint16

	return validateNode(
		pager,
		meta.RootPageID,
		InvalidPageID,
		columns,
		pkColumn,
		visited,
		&leafLevels,
	)
}

func validateNode(
	pager *Pager,
	pageID PageID,
	expectedParent PageID,
	columns []ColumnDef,
	pkColumn int,
	visited map[PageID]bool,
	leafLevels *[]uint16,
) error {

	if visited[pageID] {
		return fmt.Errorf("cycle detected at page %d", pageID)
	}

	visited[pageID] = true

	page, err := pager.ReadPage(pageID)
	if err != nil {
		return err
	}

	header, err := DecodeIndexPageHeader(page)
	if err != nil {
		return err
	}

	if header.ParentID != expectedParent {
		return fmt.Errorf(
			"page %d parent mismatch. expected=%d got=%d",
			pageID,
			expectedParent,
			header.ParentID,
		)
	}

	switch header.PageType {
	case PageTypeLeaf:
		if header.PrevLeaf != InvalidPageID {
			prevLeaf, err := pager.ReadPage(header.PrevLeaf)
			if err != nil {
				return err
			}

			prevHead, err := DecodeIndexPageHeader(prevLeaf)
			if err != nil {
				return err
			}

			if prevHead.PageType != PageTypeLeaf {
				return fmt.Errorf(
					"leaf %d PrevLeaf=%d points to non-leaf",
					header.PageID,
					header.PrevLeaf,
				)
			}

			if prevHead.NextLeaf != header.PageID {
				return fmt.Errorf(
					"prev leaf linkage broken. current=%d prev=%d prev.Next=%d",
					header.PageID,
					header.PrevLeaf,
					prevHead.NextLeaf,
				)
			}
		}

		if header.NextLeaf != InvalidPageID {
			nextLeaf, err := pager.ReadPage(header.NextLeaf)
			if err != nil {
				return err
			}

			nextHead, err := DecodeIndexPageHeader(nextLeaf)
			if err != nil {
				return err
			}

			if nextHead.PageType != PageTypeLeaf {
				return fmt.Errorf(
					"leaf %d NextLeaf=%d points to non-leaf",
					header.PageID,
					header.NextLeaf,
				)
			}

			if nextHead.PrevLeaf != header.PageID {
				return fmt.Errorf(
					"next leaf linkage broken. current=%d next=%d next.Prev=%d",
					header.PageID,
					header.NextLeaf,
					nextHead.PrevLeaf,
				)
			}
		}

		records, err := readLeafRecords(
			page,
			columns,
			pkColumn,
		)
		if err != nil {
			return err
		}

		if len(records) != int(header.RecordCount) {
			return fmt.Errorf(
				"leaf %d record count mismatch. header=%d actual=%d",
				pageID,
				header.RecordCount,
				len(records),
			)
		}

		for i := 1; i < len(records); i++ {
			if records[i-1].PK >= records[i].PK {
				return fmt.Errorf(
					"leaf %d not sorted. %d >= %d",
					pageID,
					records[i-1].PK,
					records[i].PK,
				)
			}
		}

		*leafLevels = append(*leafLevels, header.Level)

		if len(*leafLevels) > 1 {
			first := (*leafLevels)[0]

			for _, lvl := range *leafLevels {
				if lvl != first {
					return fmt.Errorf(
						"leaf levels mismatch. expected=%d got=%d",
						first,
						lvl,
					)
				}
			}
		}

		return nil

	case PageTypeInternal:
		firstChild, cells, err := readInternalCells(page)
		if err != nil {
			return err
		}

		if header.PrevLeaf != InvalidPageID || header.NextLeaf != InvalidPageID {
			return fmt.Errorf(
				"page %d is not leaf but have prev leaf id or next leaf id. expected=%d prevLeaf=%d, leafLeaf=%d",
				pageID,
				InvalidPageID,
				header.PrevLeaf,
				header.NextLeaf,
			)
		}

		if len(cells) != int(header.RecordCount) {
			return fmt.Errorf(
				"internal %d cell count mismatch. header=%d actual=%d",
				pageID,
				header.RecordCount,
				len(cells),
			)
		}

		for i := 1; i < len(cells); i++ {
			if cells[i-1].SeparatorKey >= cells[i].SeparatorKey {
				return fmt.Errorf(
					"internal %d separators not ascending",
					pageID,
				)
			}
		}

		err = validateNode(
			pager,
			firstChild,
			pageID,
			columns,
			pkColumn,
			visited,
			leafLevels,
		)
		if err != nil {
			return err
		}

		for _, cell := range cells {
			err = validateNode(
				pager,
				cell.ChildPageID,
				pageID,
				columns,
				pkColumn,
				visited,
				leafLevels,
			)
			if err != nil {
				return err
			}
		}

		return nil

	default:
		return fmt.Errorf(
			"unknown page type %d at page %d",
			header.PageType,
			pageID,
		)
	}
}


// ---------------------------------------------------------------------------
// Executor-level: memastikan Select benar-benar memfilter data nyata sesuai
// Cons -- ini yang membuktikan evaluateWhereClause/evaluatePredicate jalan,
// bukan cuma struct-nya benar dibentuk parser.
// ---------------------------------------------------------------------------

func setupWhereTestExecutor(t *testing.T, columns []ColumnDef) *Executor {
	t.Helper()

	tempDir := t.TempDir()
	config := &Config{
		DataDirectory: tempDir + string(os.PathSeparator),
		CatalogPath:   filepath.Join(tempDir, "catalog.json"),
	}
	catalog := NewCatalog()
	executor := NewExecutor(config, catalog)

	if err := executor.CreateDatabase(CreateDatabaseStatement{DBName: "testdb"}); err != nil {
		t.Fatal(err)
	}
	if err := executor.CreateTable(CreateTableStatement{
		DBName:  "testdb",
		Table:   "users",
		Columns: columns,
	}); err != nil {
		t.Fatal(err)
	}

	return executor
}

func insertRows(t *testing.T, executor *Executor, rows [][]string) {
	t.Helper()
	for _, row := range rows {
		if err := executor.Insert(InsertStatement{
			DBName: "testdb",
			Table:  "users",
			Values: row,
		}); err != nil {
			t.Fatalf("insert %v gagal: %v", row, err)
		}
	}
}

func selectPKs(t *testing.T, executor *Executor, where *WhereClause) []int32 {
	t.Helper()

	res, err := executor.Select(SelectStatement{
		DBName:  "testdb",
		Table:   "users",
		Columns: []string{"*"},
		Cons:    where,
	})
	if err != nil {
		t.Fatalf("select gagal: %v", err)
	}

	pks := make([]int32, len(res.Records))
	for i, rec := range res.Records {
		pk, ok := rec[0].Value.(int32)
		if !ok {
			t.Fatalf("record %d: PK bukan int32: %v", i, rec[0].Value)
		}
		pks[i] = pk
	}
	return pks
}

var basicUserColumns = []ColumnDef{
	{Name: "id", ValueType: IntType, Primary: true, Nullable: false},
	{Name: "name", ValueType: VarcharType, Nullable: false},
}

func TestSelectWhereEquals(t *testing.T) {
	executor := setupWhereTestExecutor(t, basicUserColumns)
	defer executor.Close()

	insertRows(t, executor, [][]string{
		{"1", "andi"},
		{"2", "budi"},
		{"3", "citra"},
	})

	pks := selectPKs(t, executor, &WhereClause{Key: "id", Op: OpEq, Val: "2"})
	assert.Equal(t, pks, []int32{2})
}

func TestSelectWhereNotEquals(t *testing.T) {
	executor := setupWhereTestExecutor(t, basicUserColumns)
	defer executor.Close()

	insertRows(t, executor, [][]string{
		{"1", "andi"},
		{"2", "budi"},
		{"3", "citra"},
	})

	pks := selectPKs(t, executor, &WhereClause{Key: "id", Op: OpNeq, Val: "2"})
	assert.Equal(t, pks, []int32{1, 3})
}

func TestSelectWhereComparisonOperators(t *testing.T) {
	executor := setupWhereTestExecutor(t, basicUserColumns)
	defer executor.Close()

	var rows [][]string
	for i := 1; i <= 10; i++ {
		rows = append(rows, []string{strconv.Itoa(i), "user" + strconv.Itoa(i)})
	}
	insertRows(t, executor, rows)

	tests := []struct {
		name string
		op   CompareOp
		val  string
		want []int32
	}{
		{"gt", OpGt, "7", []int32{8, 9, 10}},
		{"gte", OpGte, "7", []int32{7, 8, 9, 10}},
		{"lt", OpLt, "3", []int32{1, 2}},
		{"lte", OpLte, "3", []int32{1, 2, 3}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pks := selectPKs(t, executor, &WhereClause{Key: "id", Op: tc.op, Val: tc.val})
			assert.Equal(t, pks, tc.want)
		})
	}
}

func TestSelectWhereVarcharEquals(t *testing.T) {
	executor := setupWhereTestExecutor(t, basicUserColumns)
	defer executor.Close()

	insertRows(t, executor, [][]string{
		{"1", "andi"},
		{"2", "budi"},
		{"3", "andi"},
	})

	pks := selectPKs(t, executor, &WhereClause{Key: "name", Op: OpEq, Val: "andi"})
	assert.Equal(t, pks, []int32{1, 3})
}

func TestSelectWhereFloatComparison(t *testing.T) {
	columns := []ColumnDef{
		{Name: "id", ValueType: IntType, Primary: true, Nullable: false},
		{Name: "price", ValueType: FloatType, Nullable: false},
	}
	executor := setupWhereTestExecutor(t, columns)
	defer executor.Close()

	insertRows(t, executor, [][]string{
		{"1", "9.99"},
		{"2", "19.99"},
		{"3", "29.99"},
	})

	pks := selectPKs(t, executor, &WhereClause{Key: "price", Op: OpGte, Val: "19.99"})
	assert.Equal(t, pks, []int32{2, 3})
}

func TestSelectWhereBoolean(t *testing.T) {
	columns := []ColumnDef{
		{Name: "id", ValueType: IntType, Primary: true, Nullable: false},
		{Name: "active", ValueType: BooleanType, Nullable: false},
	}
	executor := setupWhereTestExecutor(t, columns)
	defer executor.Close()

	insertRows(t, executor, [][]string{
		{"1", "true"},
		{"2", "false"},
		{"3", "true"},
	})

	pks := selectPKs(t, executor, &WhereClause{Key: "active", Op: OpEq, Val: "true"})
	assert.Equal(t, pks, []int32{1, 3})

	_, err := executor.Select(SelectStatement{
		DBName:  "testdb",
		Table:   "users",
		Columns: []string{"*"},
		Cons:    &WhereClause{Key: "active", Op: OpGt, Val: "true"},
	})
	if !errors.Is(err, ErrInvalidDataType) {
		t.Fatalf("expected ErrInvalidDataType untuk operator > terhadap BOOLEAN, got: %v", err)
	}
}

// TestSelectWhereAnd menguji chain AND lewat WhereClause.Condition langsung
// (bypass parser) -- fokus ke evaluateWhereClause di executor.go.
func TestSelectWhereAnd(t *testing.T) {
	executor := setupWhereTestExecutor(t, basicUserColumns)
	defer executor.Close()

	insertRows(t, executor, [][]string{
		{"1", "andi"},
		{"2", "andi"},
		{"3", "budi"},
	})

	// id > 1 AND name = 'andi' -> hanya id=2
	pks := selectPKs(t, executor, &WhereClause{
		Key: "id", Op: OpGt, Val: "1",
		Condition: []*WhereClause{
			{Key: "name", Op: OpEq, Val: "andi", Logic: AND},
		},
	})
	assert.Equal(t, pks, []int32{2})
}

func TestSelectWhereOr(t *testing.T) {
	executor := setupWhereTestExecutor(t, basicUserColumns)
	defer executor.Close()

	insertRows(t, executor, [][]string{
		{"1", "andi"},
		{"2", "budi"},
		{"3", "citra"},
	})

	// id = 1 OR id = 3
	pks := selectPKs(t, executor, &WhereClause{
		Key: "id", Op: OpEq, Val: "1",
		Condition: []*WhereClause{
			{Key: "id", Op: OpEq, Val: "3", Logic: OR},
		},
	})
	assert.Equal(t, pks, []int32{1, 3})
}

// TestSelectWhereAndOrChainLeftToRight menegaskan aturan evaluasi
// evaluateWhereClause: "a AND b OR c" == (a AND b) OR c, kiri-ke-kanan,
// TANPA precedence AND-sebelum-OR ala SQL standar.
func TestSelectWhereAndOrChainLeftToRight(t *testing.T) {
	executor := setupWhereTestExecutor(t, basicUserColumns)
	defer executor.Close()

	insertRows(t, executor, [][]string{
		{"1", "andi"},  // a: id=1 true,  b: name=andi true  -> (a AND b)=true
		{"2", "budi"},  // a: id=1 false, b: name=andi false -> (a AND b)=false; c: id=2 true -> OR true
		{"3", "citra"}, // a false, b false -> false; c: id=2 false -> false
	})

	// id = 1 AND name = 'andi' OR id = 2
	pks := selectPKs(t, executor, &WhereClause{
		Key: "id", Op: OpEq, Val: "1",
		Condition: []*WhereClause{
			{Key: "name", Op: OpEq, Val: "andi", Logic: AND},
			{Key: "id", Op: OpEq, Val: "2", Logic: OR},
		},
	})
	assert.Equal(t, pks, []int32{1, 2})
}

func TestSelectWhereNull(t *testing.T) {
	columns := []ColumnDef{
		{Name: "id", ValueType: IntType, Primary: true, Nullable: false},
		{Name: "nickname", ValueType: VarcharType, Nullable: true},
	}
	executor := setupWhereTestExecutor(t, columns)
	defer executor.Close()

	insertRows(t, executor, [][]string{
		{"1", "NULL"},
		{"2", "bee"},
		{"3", "NULL"},
	})

	eqNull := selectPKs(t, executor, &WhereClause{Key: "nickname", Op: OpEq, Val: "NULL"})
	assert.Equal(t, eqNull, []int32{1, 3})

	neqNull := selectPKs(t, executor, &WhereClause{Key: "nickname", Op: OpNeq, Val: "NULL"})
	assert.Equal(t, neqNull, []int32{2})
}

func TestSelectWhereNullWithOrderOperatorRejected(t *testing.T) {
	columns := []ColumnDef{
		{Name: "id", ValueType: IntType, Primary: true, Nullable: false},
		{Name: "nickname", ValueType: VarcharType, Nullable: true},
	}
	executor := setupWhereTestExecutor(t, columns)
	defer executor.Close()

	insertRows(t, executor, [][]string{{"1", "NULL"}})

	_, err := executor.Select(SelectStatement{
		DBName:  "testdb",
		Table:   "users",
		Columns: []string{"*"},
		Cons:    &WhereClause{Key: "nickname", Op: OpGt, Val: "NULL"},
	})
	if !errors.Is(err, ErrInvalidDataType) {
		t.Fatalf("expected ErrInvalidDataType untuk operator > terhadap NULL, got: %v", err)
	}
}

func TestSelectWhereUnknownColumn(t *testing.T) {
	executor := setupWhereTestExecutor(t, basicUserColumns)
	defer executor.Close()

	insertRows(t, executor, [][]string{{"1", "andi"}})

	_, err := executor.Select(SelectStatement{
		DBName:  "testdb",
		Table:   "users",
		Columns: []string{"*"},
		Cons:    &WhereClause{Key: "unknown_col", Op: OpEq, Val: "1"},
	})
	if !errors.Is(err, ErrColumnNotFound) {
		t.Fatalf("expected ErrColumnNotFound, got: %v", err)
	}
}

func TestSelectWithoutWhereReturnsAll(t *testing.T) {
	executor := setupWhereTestExecutor(t, basicUserColumns)
	defer executor.Close()

	insertRows(t, executor, [][]string{
		{"1", "andi"},
		{"2", "budi"},
	})

	pks := selectPKs(t, executor, nil)
	assert.Equal(t, pks, []int32{1, 2})
}
