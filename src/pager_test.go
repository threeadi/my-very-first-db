package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-playground/assert/v2"
)

func TestAllocatePage(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "./test.3tbl")
	pager, err := CreatePager(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer pager.Close()

	page, err := pager.AllocatePage()
	if err != nil {
		t.Fatal(err)
	}
	err = pager.WritePage(page)
	if err != nil {
		t.Fatal(err)
	}
	result, err := pager.ReadPage(page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != page.ID {
		t.Errorf("page id harus identik %d", result.ID)
	}
	page2, err := pager.AllocatePage()
	if err != nil {
		t.Fatal(err)
	}

	err = pager.WritePage(page2)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, page.ID, PageID(0))

	assert.Equal(t, page2.ID, PageID(1))

	assert.NotEqual(t, page.ID, page2.ID)

	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, info.Size(), int64(2*PageSize))
}
