package main

import (
	"fmt"
	"strconv"
)

type AccessMethod int

const (
	AccessFullScan AccessMethod = iota

	AccessPKPointLookup

	AccessPKRangeScan
)

type QueryPlan struct {
	Method AccessMethod

	// Key: nilai PK yang jadi titik masuk B-tree. Dipakai oleh
	// AccessPKPointLookup dan AccessPKRangeScan saja.
	Key int32

	Criteria *WhereClause
}

func planQuery(wc *WhereClause, pkColumn string) QueryPlan {
	qp := QueryPlan{
		Method:   AccessFullScan,
		Criteria: wc,
	}
	if wc == nil || pkColumn == "" || len(wc.Criteria) > 0 {
		return qp
	}
	rawVal, ok := wc.Val.(string)
	if !ok {
		return qp
	}

	key, err := strconv.Atoi(rawVal)
	if err != nil {
		return qp
	}
	switch wc.Op {
	case OpEq:
		return QueryPlan{Method: AccessPKPointLookup, Key: int32(key), Criteria: wc}
	case OpGt, OpGte:
		return QueryPlan{Method: AccessPKRangeScan, Key: int32(key), Criteria: wc}
	default: // OpNeq, OpLt, OpLte
		return qp
	}
}

func primaryKeyColumn(columns []ColumnDef) (name string, idx int) {
	for i, col := range columns {
		if col.Primary {
			return col.Name, i
		}
	}
	return "", -1
}

// neededColumnsMask menentukan kolom mana yang harus benar-benar
// dimaterialisasi decodeRecord untuk satu SELECT -- gabungan dari
// kolom yang diminta SELECT, kolom yang dipakai WHERE (termasuk seluruh
// chain AND/OR lewat markWhereColumns), dan kolom PK (selalu ditandai,
// karena algoritma internal seperti early-stop di pointLookupPK/
// rangeScanPKForward dan urutan hasil bergantung padanya).
//
// Balik nil kalau semua kolom dibutuhkan (SELECT * dan tidak ada yang bisa
// dipangkas) -- nil punya arti khusus di decodeRecord: "materialize
// semua kolom", supaya jalur tanpa proyeksi tetap sama persis seperti
// sebelum fitur ini ada.
func neededColumnsMask(columns []ColumnDef, stmt SelectStatement, pkColIdx int) []bool {
	for _, c := range stmt.Columns {
		if c == "*" {
			return nil
		}
	}

	colIndex := make(map[string]int, len(columns))
	for i, col := range columns {
		colIndex[col.Name] = i
	}

	mask := make([]bool, len(columns))

	for _, name := range stmt.Columns {
		if idx, ok := colIndex[name]; ok {
			mask[idx] = true
			fmt.Println("masked column", name, "at index", idx)
		}
	}

	markWhereColumns(mask, colIndex, stmt.Criteria)

	if pkColIdx >= 0 {
		mask[pkColIdx] = true
	}

	return mask
}

// markWhereColumns menandai kolom mana pun yang muncul di wc atau seluruh
// chain AND/OR di wc.Condition -- rekursif, pola yang sama dengan
// evaluateWhereClause, supaya kolom yang dipakai untuk filter tetap
// didekode walau tidak ikut ditampilkan di SELECT.
func markWhereColumns(mask []bool, colIndex map[string]int, wc *WhereClause) {
	if wc == nil {
		return
	}
	if idx, ok := colIndex[wc.Key]; ok {
		mask[idx] = true
	}
	for _, next := range wc.Criteria {
		markWhereColumns(mask, colIndex, next)
	}
}

func pointLookupPK(leaf *Page, columns []ColumnDef, pkColIdx int, key int32, criteria *WhereClause, needed []bool) ([]Record, error) {
	head, err := DecodeIndexPageHeader(leaf)
	if err != nil {
		return nil, err
	}
	if head.PageType != PageTypeLeaf {
		return nil, ErrCorruptTableFile
	}

	offset := head.FirstRecordOffset
	for offset != 0 {
		record, _, nextOffset, _, err := decodeRecord(columns, leaf.Data[offset:], needed)
		if err != nil {
			return nil, err
		}

		pk, ok := record[pkColIdx].Value.(int32)
		if !ok {
			return nil, ErrInvalidDataType
		}

		if pk == key {
			match, err := evaluateWhereClause(record, columns, criteria)
			if err != nil {
				return nil, err
			}
			if match {
				return []Record{record}, nil
			}
			return nil, nil
		}

		if pk > key {
			// Leaf terurut ascending -- key dijamin tidak ada di leaf ini.
			break
		}

		offset = nextOffset
	}

	return nil, nil
}

func rangeScanPKForward(pager *Pager, startLeaf *Page, columns []ColumnDef, criteria *WhereClause, needed []bool) ([]Record, error) {
	var records []Record
	matchedOnce := false
	leaf := startLeaf

	for {
		head, err := DecodeIndexPageHeader(leaf)
		if err != nil {
			return nil, err
		}
		if head.PageType != PageTypeLeaf {
			return nil, ErrCorruptTableFile
		}

		offset := head.FirstRecordOffset
		for offset != 0 {
			record, _, nextOffset, _, err := decodeRecord(columns, leaf.Data[offset:], needed)
			if err != nil {
				return nil, err
			}

			if matchedOnce {
				records = append(records, record)
			} else {
				match, err := evaluateWhereClause(record, columns, criteria)
				if err != nil {
					return nil, err
				}
				if match {
					records = append(records, record)
					matchedOnce = true
				}
			}

			offset = nextOffset
		}

		if head.NextLeaf == InvalidPageID {
			break
		}
		leaf, err = pager.ReadPage(head.NextLeaf)
		if err != nil {
			return nil, err
		}
	}

	return records, nil
}
