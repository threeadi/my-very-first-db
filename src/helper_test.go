package main

import (
	"errors"
	"math"
	"testing"
)

// ---------------------------------------------------------------------------
// Konstanta identitas file.
//
// ExpectedMagic  : penanda "file ini dibuat oleh engine ini". Ditulis sekali
//
//	saat CREATE TABLE/DATABASE, tidak pernah berubah setelahnya
//	kecuali kamu sengaja mengganti identitas format total.
//
// CurrentVersion : versi layout biner yang dipahami oleh binary yang sedang
//
//	jalan. Naikkan HANYA saat layout MetaPage/IndexPageHeader/
//	record berubah sehingga file lama tidak bisa dibaca lurus
//	oleh kode baru tanpa migrasi.
//
// ---------------------------------------------------------------------------
var ExpectedMagic = [4]byte{'R', 'D', 'B', '1'}

const CurrentVersion uint8 = 1

// Sentinel errors baru. Kalau nama-nama ini sudah ada di errors.go kamu,
// hapus deklarasi di sini supaya tidak duplicate declaration.
var (
	ErrInvalidMagicNumber = errors.New("invalid magic number: file bukan database yang dikenali")
	ErrUnsupportedVersion = errors.New("unsupported file version")
	ErrPageSizeMismatch   = errors.New("page size pada meta page tidak cocok dengan konfigurasi engine")
)

// NewMetaPage membangun MetaPage baru dengan magic number dan version yang
// benar secara otomatis, supaya pemanggil (mis. saat CREATE TABLE) tidak
// perlu—dan tidak bisa—salah isi Magic/Version secara manual.
func NewMetaPage(rootPageID PageID, nextPageID PageID) MetaPage {
	return MetaPage{
		Magic:      ExpectedMagic,
		Version:    CurrentVersion,
		PageSize:   uint16(PageSize),
		RootPageID: rootPageID,
		NextPageID: nextPageID,
	}
}

// DecodeMetaPageStrict adalah versi DecodeMetaPage yang menolak file dengan
// magic number, version, atau page size yang tidak dikenali. Gunakan ini di
// jalur "open table/database", bukan di jalur internal yang sudah tahu page
// itu valid.
//
// Kalau kamu lebih suka menyatukan validasi ini langsung ke dalam
// DecodeMetaPage yang sudah ada (disarankan, supaya tidak ada jalur baca
// meta page yang lupa validasi), tinggal tempel blok validasi di bawah ini
// persis setelah masing-masing field selesai di-decode.
func DecodeMetaPageStrict(page *Page) (MetaPage, error) {
	meta, err := DecodeMetaPage(page)
	if err != nil {
		return meta, err
	}

	if meta.Magic != ExpectedMagic {
		return meta, ErrInvalidMagicNumber
	}

	if meta.Version != CurrentVersion {
		return meta, ErrUnsupportedVersion
	}

	if meta.PageSize != uint16(PageSize) {
		return meta, ErrPageSizeMismatch
	}

	return meta, nil
}

func TestEncodeDecodeRecord_RoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		columns []ColumnDef
		values  []ParsedValue
	}{
		{
			name: "all types populated",
			columns: []ColumnDef{
				{Name: "id", ValueType: IntType, Nullable: false},
				{Name: "price", ValueType: FloatType, Nullable: false},
				{Name: "active", ValueType: BooleanType, Nullable: false},
				{Name: "label", ValueType: VarcharType, Nullable: false},
			},
			values: []ParsedValue{
				{ColName: "id", Value: int32(42)},
				{ColName: "price", Value: float32(19.99)},
				{ColName: "active", Value: true},
				{ColName: "label", Value: "hello"},
			},
		},
		{
			name: "nullable columns with NULL",
			columns: []ColumnDef{
				{Name: "id", ValueType: IntType, Nullable: false},
				{Name: "nickname", ValueType: VarcharType, Nullable: true},
				{Name: "score", ValueType: FloatType, Nullable: true},
			},
			values: []ParsedValue{
				{ColName: "id", Value: int32(1)},
				{ColName: "nickname", Value: nil},
				{ColName: "score", Value: nil},
			},
		},
		{
			// Beda kasus dari NULL: string kosong tetap harus punya panjang 0
			// tapi Null == false.
			name: "empty string vs null distinction",
			columns: []ColumnDef{
				{Name: "id", ValueType: IntType, Nullable: false},
				{Name: "note", ValueType: VarcharType, Nullable: true},
			},
			values: []ParsedValue{
				{ColName: "id", Value: int32(2)},
				{ColName: "note", Value: ""},
			},
		},
		{
			name: "int32 boundary values",
			columns: []ColumnDef{
				{Name: "min_val", ValueType: IntType, Nullable: false},
				{Name: "max_val", ValueType: IntType, Nullable: false},
			},
			values: []ParsedValue{
				{ColName: "min_val", Value: int32(math.MinInt32)},
				{ColName: "max_val", Value: int32(math.MaxInt32)},
			},
		},
		{
			name: "float32 assorted values including negative and tiny",
			columns: []ColumnDef{
				{Name: "zero", ValueType: FloatType, Nullable: false},
				{Name: "neg", ValueType: FloatType, Nullable: false},
				{Name: "small", ValueType: FloatType, Nullable: false},
			},
			values: []ParsedValue{
				{ColName: "zero", Value: float32(0)},
				{ColName: "neg", Value: float32(-1234.5678)},
				{ColName: "small", Value: float32(0.0000001)},
			},
		},
		{
			name: "boolean both values",
			columns: []ColumnDef{
				{Name: "flag_true", ValueType: BooleanType, Nullable: false},
				{Name: "flag_false", ValueType: BooleanType, Nullable: false},
			},
			values: []ParsedValue{
				{ColName: "flag_true", Value: true},
				{ColName: "flag_false", Value: false},
			},
		},
		{
			// Batas atas panjang VARCHAR yang didukung oleh uint16 length prefix.
			name: "varchar at max uint16 length",
			columns: []ColumnDef{
				{Name: "big", ValueType: VarcharType, Nullable: false},
			},
			values: []ParsedValue{
				{ColName: "big", Value: string(make([]byte, 0xffff))},
			},
		},
		{
			// Memastikan nullIdx di setNullBit/isNullBitSet tidak salah offset
			// ketika kolom nullable & non-nullable diselang-seling.
			name: "mixed nullable offsets do not shift",
			columns: []ColumnDef{
				{Name: "a", ValueType: IntType, Nullable: true},
				{Name: "b", ValueType: IntType, Nullable: false},
				{Name: "c", ValueType: IntType, Nullable: true},
				{Name: "d", ValueType: IntType, Nullable: true},
			},
			values: []ParsedValue{
				{ColName: "a", Value: nil},
				{ColName: "b", Value: int32(5)},
				{ColName: "c", Value: int32(6)},
				{ColName: "d", Value: nil},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const wantNextOff uint16 = 777

			encoded, err := encodeRecord(tc.columns, tc.values, wantNextOff)
			if err != nil {
				t.Fatalf("encodeRecord failed: %v", err)
			}

			decoded, _, gotNextOff, recordSize, err := decodeRecord(tc.columns, encoded)
			if err != nil {
				t.Fatalf("decodeRecord failed: %v", err)
			}

			if gotNextOff != wantNextOff {
				t.Errorf("NextOffset round-trip: got %d, want %d", gotNextOff, wantNextOff)
			}

			if recordSize != len(encoded) {
				t.Errorf("recordSize = %d, want %d (len of encoded bytes)", recordSize, len(encoded))
			}

			valuesByCol := make(map[string]ParsedValue, len(tc.values))
			for _, v := range tc.values {
				valuesByCol[v.ColName] = v
			}

			for idx, col := range tc.columns {
				want, hasWant := valuesByCol[col.Name]
				got := decoded[idx]

				wantIsNull := !hasWant || want.Value == nil

				if got.Null != wantIsNull {
					t.Errorf("column %s: Null = %v, want %v", col.Name, got.Null, wantIsNull)
					continue
				}

				if wantIsNull {
					continue
				}

				if col.ValueType == FloatType {
					gotF, _ := got.Value.(float32)
					wantF, _ := want.Value.(float32)
					if gotF != wantF {
						t.Errorf("column %s: got %v, want %v", col.Name, gotF, wantF)
					}
					continue
				}

				if got.Value != want.Value {
					t.Errorf("column %s: got %v (%T), want %v (%T)",
						col.Name, got.Value, got.Value, want.Value, want.Value)
				}
			}
		})
	}
}

func TestEncodeRecord_NotNullViolation(t *testing.T) {
	columns := []ColumnDef{
		{Name: "id", ValueType: IntType, Nullable: false},
	}
	values := []ParsedValue{
		{ColName: "id", Value: nil},
	}

	_, err := encodeRecord(columns, values, 0)
	if !errors.Is(err, ErrNotNullViolation) {
		t.Fatalf("expected ErrNotNullViolation, got %v", err)
	}
}

func TestEncodeRecord_MissingRequiredColumn(t *testing.T) {
	// Kolom non-nullable yang sama sekali tidak ada di values (bukan cuma nil)
	// harus tetap kena NotNullViolation, bukan silently defaulted.
	columns := []ColumnDef{
		{Name: "id", ValueType: IntType, Nullable: false},
	}
	values := []ParsedValue{}

	_, err := encodeRecord(columns, values, 0)
	if !errors.Is(err, ErrNotNullViolation) {
		t.Fatalf("expected ErrNotNullViolation for missing column, got %v", err)
	}
}

func TestEncodeRecord_StringTooLong(t *testing.T) {
	columns := []ColumnDef{
		{Name: "label", ValueType: VarcharType, Nullable: false},
	}
	values := []ParsedValue{
		// 65536 byte, melewati batas uint16 (0xffff = 65535).
		{ColName: "label", Value: string(make([]byte, 0x10000))},
	}

	_, err := encodeRecord(columns, values, 0)
	if !errors.Is(err, ErrStringTooLong) {
		t.Fatalf("expected ErrStringTooLong, got %v", err)
	}
}

func TestDecodeRecord_TruncatedData(t *testing.T) {
	columns := []ColumnDef{
		{Name: "id", ValueType: IntType, Nullable: false},
	}
	values := []ParsedValue{
		{ColName: "id", Value: int32(1)},
	}

	encoded, err := encodeRecord(columns, values, 0)
	if err != nil {
		t.Fatalf("encodeRecord failed: %v", err)
	}

	// Potong byte terakhir supaya field IntType tidak lengkap.
	truncated := encoded[:len(encoded)-1]

	_, _, _, _, err = decodeRecord(columns, truncated)
	if err == nil {
		t.Fatal("expected error decoding truncated record, got nil")
	}
}
