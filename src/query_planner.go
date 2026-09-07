package main

import "strconv"

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

func pointLookupPK(leaf *Page, columns []ColumnDef, pkColIdx int, key int32, criteria *WhereClause) ([]Record, error) {
	head, err := DecodeIndexPageHeader(leaf)
	if err != nil {
		return nil, err
	}
	if head.PageType != PageTypeLeaf {
		return nil, ErrCorruptTableFile
	}

	offset := head.FirstRecordOffset
	for offset != 0 {
		record, _, nextOffset, _, err := decodeRecord(columns, leaf.Data[offset:])
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

func rangeScanPKForward(pager *Pager, startLeaf *Page, columns []ColumnDef, criteria *WhereClause) ([]Record, error) {
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
			record, _, nextOffset, _, err := decodeRecord(columns, leaf.Data[offset:])
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
