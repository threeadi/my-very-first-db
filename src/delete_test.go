package main

import (
	"strconv"
	"testing"

	"github.com/go-playground/assert/v2"
)

func TestDelete_MatchingRowsHiddenFromSelect(t *testing.T) {
	executor := setupWhereTestExecutor(t, basicUserColumns)
	defer executor.Close()

	insertRows(t, executor, [][]string{
		{"1", "andi"},
		{"2", "budi"},
		{"3", "citra"},
	})

	n, err := executor.Delete(DeleteStatement{
		DBName:   "testdb",
		Table:    "users",
		Criteria: &WhereClause{Key: "id", Op: OpEq, Val: "2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, n, 1)

	pks := selectPKs(t, executor, nil)
	assert.Equal(t, pks, []int32{1, 3})
}

func TestDelete_WithoutWhereDeletesAll(t *testing.T) {
	executor := setupWhereTestExecutor(t, basicUserColumns)
	defer executor.Close()

	insertRows(t, executor, [][]string{
		{"1", "andi"},
		{"2", "budi"},
		{"3", "citra"},
	})

	n, err := executor.Delete(DeleteStatement{
		DBName: "testdb",
		Table:  "users",
	})
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, n, 3)

	pks := selectPKs(t, executor, nil)
	if len(pks) != 0 {
		t.Fatalf("expected tabel kosong setelah delete tanpa WHERE, got: %v", pks)
	}
}

func TestDelete_NoMatchReturnsZero(t *testing.T) {
	executor := setupWhereTestExecutor(t, basicUserColumns)
	defer executor.Close()

	insertRows(t, executor, [][]string{{"1", "andi"}})

	n, err := executor.Delete(DeleteStatement{
		DBName:   "testdb",
		Table:    "users",
		Criteria: &WhereClause{Key: "id", Op: OpEq, Val: "999"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, n, 0)

	pks := selectPKs(t, executor, nil)
	assert.Equal(t, pks, []int32{1})
}

func TestDelete_TombstoneHiddenFromPointLookup(t *testing.T) {
	executor := setupWhereTestExecutor(t, basicUserColumns)
	defer executor.Close()

	insertRows(t, executor, [][]string{
		{"1", "andi"},
		{"2", "budi"},
		{"3", "citra"},
	})

	n, err := executor.Delete(DeleteStatement{
		DBName:   "testdb",
		Table:    "users",
		Criteria: &WhereClause{Key: "id", Op: OpEq, Val: "2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, n, 1)

	// WHERE id = 2 -- planQuery memilih AccessPKPointLookup, harus tetap
	// mengabaikan tombstone-nya sendiri, bukan cuma di full scan.
	pks := selectPKs(t, executor, &WhereClause{Key: "id", Op: OpEq, Val: "2"})
	if len(pks) != 0 {
		t.Fatalf("expected tombstoned row tidak ditemukan via point lookup, got: %v", pks)
	}

	stillThere := selectPKs(t, executor, &WhereClause{Key: "id", Op: OpEq, Val: "3"})
	assert.Equal(t, stillThere, []int32{3})
}

func TestDelete_TombstoneHiddenFromRangeScan(t *testing.T) {
	executor := setupWhereTestExecutor(t, basicUserColumns)
	defer executor.Close()

	var rows [][]string
	for i := 1; i <= 10; i++ {
		rows = append(rows, []string{strconv.Itoa(i), "user" + strconv.Itoa(i)})
	}
	insertRows(t, executor, rows)

	// Hapus row genap di tengah range yang nanti di-scan.
	for _, id := range []string{"6", "8"} {
		if _, err := executor.Delete(DeleteStatement{
			DBName:   "testdb",
			Table:    "users",
			Criteria: &WhereClause{Key: "id", Op: OpEq, Val: id},
		}); err != nil {
			t.Fatal(err)
		}
	}

	// WHERE id > 5 -- planQuery memilih AccessPKRangeScan; 6 dan 8 sudah
	// tombstoned jadi tidak boleh muncul.
	pks := selectPKs(t, executor, &WhereClause{Key: "id", Op: OpGt, Val: "5"})
	assert.Equal(t, pks, []int32{7, 9, 10})
}

func TestDelete_ThenReinsertSamePK(t *testing.T) {
	executor := setupWhereTestExecutor(t, basicUserColumns)
	defer executor.Close()

	insertRows(t, executor, [][]string{
		{"1", "andi"},
		{"2", "budi"},
	})

	if _, err := executor.Delete(DeleteStatement{
		DBName:   "testdb",
		Table:    "users",
		Criteria: &WhereClause{Key: "id", Op: OpEq, Val: "1"},
	}); err != nil {
		t.Fatal(err)
	}

	// PK 1 sudah tombstoned -- insert ulang dengan PK yang sama harus
	// diterima, BUKAN ditolak sebagai duplicate.
	if err := executor.Insert(InsertStatement{
		DBName: "testdb",
		Table:  "users",
		Values: []string{"1", "andi baru"},
	}); err != nil {
		t.Fatalf("insert ulang PK yang sudah di-DELETE seharusnya berhasil: %v", err)
	}

	res, err := executor.Select(SelectStatement{
		DBName:  "testdb",
		Table:   "users",
		Columns: []string{"*"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Full scan harus hanya melihat SATU record hidup untuk PK 1, dengan
	// data yang baru -- bukan dua record (tombstone lama + record baru).
	found := 0
	for _, rec := range res.Records {
		if rec[0].Value.(int32) == 1 {
			found++
			assert.Equal(t, rec[1].Value.(string), "andi baru")
		}
	}
	assert.Equal(t, found, 1)

	// Point lookup juga harus melewati tombstone lama dan menemukan record
	// barunya, bukan berhenti di tombstone dan bilang "tidak ketemu".
	viaLookup := selectPKs(t, executor, &WhereClause{Key: "id", Op: OpEq, Val: "1"})
	assert.Equal(t, viaLookup, []int32{1})

	n, err := executor.Delete(DeleteStatement{
		DBName:   "testdb",
		Table:    "users",
		Criteria: &WhereClause{Key: "id", Op: OpEq, Val: "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, n, 1)
}
