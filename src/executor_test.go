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
