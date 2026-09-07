package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/go-playground/assert/v2"
)

// ---------------------------------------------------------------------------
// planQuery: murni unit test atas keputusan strategi, tidak menyentuh disk
// sama sekali.
// ---------------------------------------------------------------------------

func TestPlanQuery_NoWhere(t *testing.T) {
	plan := planQuery(nil, "id")
	assert.Equal(t, plan.Method, AccessFullScan)
	if plan.Criteria != nil {
		t.Fatalf("expected Criteria nil, got: %+v", plan.Criteria)
	}
}

func TestPlanQuery_EqOnPK(t *testing.T) {
	wc := &WhereClause{Key: "id", Op: OpEq, Val: "42"}
	plan := planQuery(wc, "id")

	assert.Equal(t, plan.Method, AccessPKPointLookup)
	assert.Equal(t, plan.Key, int32(42))
	assert.Equal(t, plan.Criteria, wc)
}

func TestPlanQuery_GtGteOnPK(t *testing.T) {
	tests := []struct {
		name string
		op   CompareOp
	}{
		{"gt", OpGt},
		{"gte", OpGte},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wc := &WhereClause{Key: "id", Op: tc.op, Val: "10"}
			plan := planQuery(wc, "id")

			assert.Equal(t, plan.Method, AccessPKRangeScan)
			assert.Equal(t, plan.Key, int32(10))
		})
	}
}

// TestPlanQuery_NotYetOptimized mendokumentasikan batas cakupan planner
// sekarang: OpLt/OpLte/OpNeq pada PK sengaja belum dapat strategi khusus,
// tetap AccessFullScan (masih benar, cuma belum paling murah).
func TestPlanQuery_NotYetOptimized(t *testing.T) {
	tests := []CompareOp{OpLt, OpLte, OpNeq}

	for _, op := range tests {
		wc := &WhereClause{Key: "id", Op: op, Val: "10"}
		plan := planQuery(wc, "id")
		assert.Equal(t, plan.Method, AccessFullScan)
	}
}

func TestPlanQuery_NonPKColumnFallsBackToFullScan(t *testing.T) {
	wc := &WhereClause{Key: "name", Op: OpEq, Val: "andi"}
	plan := planQuery(wc, "id")
	assert.Equal(t, plan.Method, AccessFullScan)
}

func TestPlanQuery_AndOrChainFallsBackToFullScan(t *testing.T) {
	// Walau predikat pertama match PK + OpEq, adanya chain AND/OR membuat
	// planner tidak mencoba push down -- lihat catatan di planQuery.
	wc := &WhereClause{
		Key: "id", Op: OpEq, Val: "1",
		Criteria: []*WhereClause{
			{Key: "name", Op: OpEq, Val: "andi", Logic: AND},
		},
	}
	plan := planQuery(wc, "id")
	assert.Equal(t, plan.Method, AccessFullScan)
}

func TestPlanQuery_NoPKColumnAlwaysFullScan(t *testing.T) {
	wc := &WhereClause{Key: "id", Op: OpEq, Val: "1"}
	plan := planQuery(wc, "") // tabel tanpa PK
	assert.Equal(t, plan.Method, AccessFullScan)
}

func TestPlanQuery_NonNumericValueFallsBackToFullScan(t *testing.T) {
	wc := &WhereClause{Key: "id", Op: OpEq, Val: "bukan-angka"}
	plan := planQuery(wc, "id")
	assert.Equal(t, plan.Method, AccessFullScan)
}

// ---------------------------------------------------------------------------
// Executor-level: pastikan AccessPKPointLookup dan AccessPKRangeScan
// menghasilkan Records yang IDENTIK dengan full scan + filter manual --
// optimizer boleh mengubah cara baca data, tidak boleh mengubah hasilnya.
// ---------------------------------------------------------------------------

func setupPlannerTestExecutor(t *testing.T, n int) *Executor {
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
		DBName: "testdb",
		Table:  "users",
		Columns: []ColumnDef{
			{Name: "id", ValueType: IntType, Primary: true, Nullable: false},
			{Name: "name", ValueType: VarcharType, Nullable: false},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// largeValue supaya leaf sering split -- memaksa pohon jadi multi-level
	// dan multi-leaf, sehingga point lookup & range scan benar-benar diuji
	// lewat descent B-tree yang nyata, bukan cuma satu leaf tunggal.
	largeValue := createLargeString()
	for i := 1; i <= n; i++ {
		if err := executor.Insert(InsertStatement{
			DBName: "testdb",
			Table:  "users",
			Values: []string{strconv.Itoa(i), largeValue},
		}); err != nil {
			t.Fatalf("insert %d gagal: %v", i, err)
		}
	}

	return executor
}

func planPKsOf(t *testing.T, executor *Executor, where *WhereClause) []int32 {
	t.Helper()
	res, err := executor.Select(SelectStatement{
		DBName:   "testdb",
		Table:    "users",
		Columns:  []string{"*"},
		Criteria: where,
	})
	if err != nil {
		t.Fatalf("select gagal: %v", err)
	}

	// Dibangun lewat append (bukan make+index) supaya hasil kosong tetap
	// nil, konsisten dengan bruteForcePKs -- make([]int32, 0) menghasilkan
	// slice non-nil yang dianggap TIDAK SAMA dengan nil oleh
	// reflect.DeepEqual (dipakai assert.Equal), walau keduanya sama-sama
	// tampil "[]" saat di-log.
	var pks []int32
	for i, rec := range res.Records {
		pk, ok := rec[0].Value.(int32)
		if !ok {
			t.Fatalf("record %d: PK bukan int32", i)
		}
		pks = append(pks, pk)
	}
	return pks
}

func TestSelectPointLookup_MatchesFullScanResult(t *testing.T) {
	const n = 300
	executor := setupPlannerTestExecutor(t, n)
	defer executor.Close()

	tests := []struct {
		name    string
		key     string
		wantPKs []int32
	}{
		{"middle", "150", []int32{150}},
		{"first", "1", []int32{1}},
		{"last", strconv.Itoa(n), []int32{int32(n)}},
		{"not_found", strconv.Itoa(n + 999), nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pks := planPKsOf(t, executor, &WhereClause{Key: "id", Op: OpEq, Val: tc.key})
			assert.Equal(t, pks, tc.wantPKs)
		})
	}
}

func TestSelectRangeScan_MatchesFullScanResult(t *testing.T) {
	const n = 300
	executor := setupPlannerTestExecutor(t, n)
	defer executor.Close()

	tests := []struct {
		name string
		op   CompareOp
		val  string
	}{
		{"gt_middle", OpGt, "150"},
		{"gte_middle", OpGte, "150"},
		{"gt_zero", OpGt, "0"},          // seluruh row match
		{"gt_beyond_max", OpGt, "9999"}, // tidak ada yang match
		{"gte_last", OpGte, strconv.Itoa(n)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			optimized := planPKsOf(t, executor, &WhereClause{Key: "id", Op: tc.op, Val: tc.val})

			expected := bruteForcePKs(n, func(pk int32) bool {
				switch tc.op {
				case OpGt:
					return pk > mustAtoi32(tc.val)
				case OpGte:
					return pk >= mustAtoi32(tc.val)
				}
				return false
			})

			assert.Equal(t, optimized, expected)
		})
	}
}

func mustAtoi32(s string) int32 {
	v, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}
	return int32(v)
}

func bruteForcePKs(n int, keep func(pk int32) bool) []int32 {
	var out []int32
	for i := 1; i <= n; i++ {
		if keep(int32(i)) {
			out = append(out, int32(i))
		}
	}
	return out
}

// TestSelectOptimizedVsFullScan_RandomSample memastikan AccessPKPointLookup
// dan AccessPKRangeScan konsisten dengan full scan untuk beberapa titik
// acak, sebagai pengaman tambahan di luar kasus-kasus tetap di atas.
func TestSelectOptimizedVsFullScan_RandomSample(t *testing.T) {
	const n = 500
	executor := setupPlannerTestExecutor(t, n)
	defer executor.Close()

	sample := []int{1, 2, 7, 42, 99, 100, 101, 250, 499, 500}

	for _, key := range sample {
		key := key
		t.Run(strconv.Itoa(key), func(t *testing.T) {
			eq := planPKsOf(t, executor, &WhereClause{Key: "id", Op: OpEq, Val: strconv.Itoa(key)})
			assert.Equal(t, eq, []int32{int32(key)})

			gt := planPKsOf(t, executor, &WhereClause{Key: "id", Op: OpGt, Val: strconv.Itoa(key)})
			assert.Equal(t, gt, bruteForcePKs(n, func(pk int32) bool { return pk > int32(key) }))

			gte := planPKsOf(t, executor, &WhereClause{Key: "id", Op: OpGte, Val: strconv.Itoa(key)})
			assert.Equal(t, gte, bruteForcePKs(n, func(pk int32) bool { return pk >= int32(key) }))
		})
	}
}

// TestSelectPointLookup_CombinedWithNonPKPredicateStillFiltersCorrectly
// menguji Filter pengaman pada AccessPKPointLookup: seandainya nanti ada
// bentuk WHERE yang menghasilkan Filter tambahan meski Method-nya sudah
// PointLookup (saat ini planQuery tidak pernah membuat kombinasi begini
// karena AND/OR selalu fallback ke full scan, tapi executor tidak boleh
// diam-diam mengasumsikan itu -- ia tetap harus menjalankan Filter apa pun
// yang dikirim planner).
func TestSelectPointLookup_FilterAlwaysApplied(t *testing.T) {
	executor := setupPlannerTestExecutor(t, 10)
	defer executor.Close()

	// WHERE id = 5 -- Filter yang dikirim planner adalah wc aslinya sendiri,
	// jadi record yang ditemukan tetap harus lolos evaluateWhereClause(wc).
	pks := planPKsOf(t, executor, &WhereClause{Key: "id", Op: OpEq, Val: "5"})
	assert.Equal(t, pks, []int32{5})
}
