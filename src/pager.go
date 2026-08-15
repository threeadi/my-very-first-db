package main

import (
	"errors"
	"io"
	"os"
)

const PageSize uint16 = 4096 // 4 KB

const IndexPageHeaderSize = 17 // 17 byte

type PageID uint32

type Page struct {
	ID   PageID
	Data [PageSize]byte
}

type MetaPage struct {
	Magic      [4]byte
	Version    uint8
	PageSize   uint16
	RootPageID PageID
	NextPageID PageID
}

type PageType uint8

const (
	PageTypeInternal PageType = iota
	PageTypeLeaf
)

const InvalidPageID PageID = ^PageID(0)

// 17 bytes header
type IndexPageHeader struct {
	PageType          PageType
	PageID            PageID
	ParentID          PageID
	Level             uint16
	RecordCount       uint16
	FirstRecordOffset uint16
	FreeStart         uint16
}

type Pager struct {
	file *os.File
}

func OpenPager(path string) (*Pager, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}

	return &Pager{
		file: file,
	}, nil
}

func CreatePager(path string) (*Pager, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0644)
	if err != nil {
		return nil, err
	}
	return &Pager{file}, nil
}

func (p *Pager) ReadPage(id PageID) (*Page, error) {
	page := Page{
		ID: id,
	}

	n, err := p.file.ReadAt(page.Data[:], int64(id)*int64(PageSize))
	if err != nil {
		if errors.Is(err, io.EOF) && n > 0 {
			return nil, ErrCorruptTableFile
		}

		return nil, err
	}

	if uint16(n) != PageSize {
		return nil, ErrCorruptTableFile
	}

	return &page, nil
}

func (p *Pager) WritePage(page *Page) error {
	off := int64(page.ID) * int64(PageSize)
	n, err := p.file.WriteAt(page.Data[:], off)
	if err != nil {
		return err
	}
	if uint16(n) != PageSize {
		return ErrPageWriteFailed
	}
	return nil
}

func (p *Pager) AllocatePage() (*Page, error) {
	info, err := p.file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size()%int64(PageSize) != 0 {
		return nil, ErrCorruptTableFile
	}

	data := [PageSize]byte{}

	page := Page{
		ID:   PageID(info.Size() / int64(PageSize)),
		Data: data,
	}

	return &page, nil
}

func (p *Pager) Close() error {
	return p.file.Close()
}
