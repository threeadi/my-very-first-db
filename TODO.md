# Roadmap Belajar Go: Membuat RDBMS Mini

Dokumen ini adalah peta belajar dan checklist.

## Status audit — 15 Agustus 2026

Legenda:

- `[x]` selesai dan sudah terlihat pada implementasi;
- `[~]` sebagian sudah ada, tetapi belum memenuhi seluruh kontrak atau masih memiliki bug;
- `[ ]` belum dikerjakan.

### Ringkasan kondisi saat ini

- Sudah ada REPL, config loader, lexer, parser AST, catalog JSON, `CREATE DATABASE`, `CREATE TABLE`, `INSERT`, dan `SELECT`.
- Storage tabel sudah berpindah ke **page-based 4096 byte** dengan `Pager`, `PageID`, `ReadPage`, `WritePage`, dan alokasi page.
- Page 0 adalah `MetaPage`; root B+ tree dibaca melalui `MetaPage.RootPageID`. Page 1+ dipakai sebagai page B+ tree.
- Root awal berupa satu **leaf page** kosong dengan `IndexPageHeader`.
- Format record v1 sudah menjadi: varlen metadata → null bitmap → flags → `NextOffset` → payload kolom.
- `encodeRecord()` dan `decodeRecord()` sudah round-trip melalui file; data tetap dapat dibaca setelah program ditutup dan dibuka kembali.
- `SELECT *` sudah membaca record dari leaf melalui `FirstRecordOffset` lalu mengikuti `NextOffset`.
- `INSERT` pada satu leaf sudah menjaga **urutan logis PRIMARY KEY** walaupun posisi fisik record append ke `FreeStart`.
- Insert depan, tengah, dan belakang menggunakan pasangan `prevOffset` / `currentOffset`; predecessor di-patch agar menunjuk ke record baru.
- Duplicate primary key sudah ditolak dan constraint `NOT NULL` sudah tervalidasi saat insert.
- **B+ tree multi-level sudah diimplementasikan**: `targetPage()` (traversal root → internal → leaf), `scanTree()` (full scan rekursif leaf+internal), `splitLeaf()` (split leaf non-root + insert separator ke parent), `splitRootLeaf()` (split root leaf → buat root internal baru), dan `splitRootInternal()` (split root internal saat penuh).
- Format internal page sudah ada: `firstChild` (4 byte) + sel ``InternalCell{SeparatorKey, ChildPageID}`` (8 byte), dibaca/tulis via `readInternalCells()` / `rewriteInternal()` / `insertInternalCell()`.

## Tujuan akhir

Database harus mampu:

- membuat dan menghapus database;
- membuat dan menghapus tabel;
- menjalankan `INSERT`, `SELECT`, `UPDATE`, dan `DELETE`;
- memilih kolom tertentu;
- menjalankan agregasi `SUM`;
- menjalankan `ORDER BY`;
- menjalankan `GROUP BY`;
- membuat index satu kolom;
- mendukung index biasa dan index unik.

## Batasan versi pertama

Agar proyek selesai dan tetap bisa dipahami:

- gunakan satu proses dan satu pengguna;
- jalankan satu query pada satu waktu;
- belum ada transaksi paralel (belum perlu goroutine/concurrency di versi ini);
- belum ada `JOIN`, foreign key, subquery, atau multi-column index;
- tipe data awal cukup integer dan text;
- satu tabel disimpan dalam satu file data;
- satu index disimpan dalam satu file index;
- SQL boleh berupa subset kecil, tidak harus kompatibel penuh dengan PostgreSQL;
- utamakan hasil yang benar sebelum optimasi.

## Gambaran arsitektur

Aliran sebuah perintah:

1. **REPL/CLI** menerima teks dari pengguna.
2. **Lexer** memecah teks menjadi token.
3. **Parser** mengubah token menjadi statement terstruktur.
4. **Binder/semantic checker** memastikan database, tabel, dan kolom memang ada serta tipe datanya sesuai.
5. **Planner** memilih operasi yang diperlukan dan memutuskan table scan atau index scan.
6. **Executor** menjalankan operasi.
7. **Storage engine** membaca atau menulis page dan record.
8. **Catalog** menyimpan metadata database, tabel, kolom, dan index.
9. **Result formatter** menampilkan kolom dan baris hasil.

Untuk query `SELECT`, aliran data logisnya:

`scan → filter → group/agregasi → projection → sort → output`

Catatan: susunan ini adalah model belajar. Nanti planner boleh mengubah urutan internal bila hasilnya tetap benar.

## Keputusan desain yang harus ditulis sebelum coding

- [x] Tentukan sintaks SQL subset yang didukung.
- [x] Tentukan aturan nama database, tabel, kolom, dan index.
- [x] Tentukan apakah nama bersifat case-sensitive.
- [x] Tentukan tipe data awal dan representasi nilai `NULL`.
- [~] Tentukan batas ukuran text dan ukuran page. — `PageSize = 4096` sudah aktif dan insert menolak record yang melewati batas page; batas maksimum VARCHAR/record formal masih perlu diselaraskan dengan kontrak storage.
- [~] Tentukan format direktori dan nama file di disk. — Sudah ada `DataDirectory`, `CatalogPath`, dan ekstensi `.3tbl`; path creation masih perlu dirapikan dengan `filepath.Join`.
- [~] Tentukan format metadata katalog dan nomor versinya. — Catalog JSON sudah ada, tetapi belum memiliki `catalog_version` dan metadata primary/index.
- [x] Tentukan format record: varlen metadata, null bitmap, flags, `NextOffset`, lalu payload kolom. — Sudah dipakai oleh `encodeRecord()` dan `decodeRecord()`.
- [x] Tentukan identitas stabil sebuah record. — Untuk clustered tree, **primary key adalah identitas logis**; offset record di page hanya lokasi fisik yang dapat berubah.
- [~] Tentukan perilaku error untuk objek yang sudah ada atau tidak ditemukan. — Sentinel error sudah ada, tetapi masih bercampur dengan `panic`, `log.Fatal`, dan raw I/O error.
- [ ] Tentukan kapan perubahan dipaksa tersimpan ke disk dengan `File.Sync()` dan urutan flush meta/data.

---

## Milestone 0 — Dasar Go yang diperlukan

Pelajari sambil membuat eksperimen kecil terpisah dari mesin database.

- [~] Memahami value semantics vs pointer (`*T`), kapan pakai pointer receiver vs value receiver. — Sudah dipakai pada `Lexer`, `Parser`, `Executor`, dan `Catalog`; perlu test/penjelasan ownership.
- [~] Memahami `struct`, method, dan embedding (Go tidak punya class/inheritance klasik). — Struct dan method sudah dipakai; embedding belum dipraktikkan.
- [x] Memahami representasi "enum" di Go: `const` + `iota`, dan type switch untuk pattern-matching sederhana.
- [~] Memahami idiom `(value, error)` sebagai pengganti `Option`/`Result`, termasuk `errors.Is`, `errors.As`, dan `fmt.Errorf("...: %w", err)`. — `errors.Is`, `errors.Join`, dan `%w` sudah dipakai; `errors.As` dan error boundary belum.
- [~] Memahami collections: slice, map, dan cara menjaga urutan. — Slice/map sudah dipakai; ordered collection belum.
- [x] Memahami `string`, `[]byte`, `bytes.Buffer`, dan `encoding/binary` untuk konversi byte.
- [~] Memahami interface dan generics (`type Foo[T any] struct{...}`) secukupnya. — Interface `Statement` dan `DBLogger` sudah ada; generics belum diperlukan/dipraktikkan.
- [~] Memahami package dan visibility. — Exported/unexported identifier sudah dipakai, tetapi seluruh project masih berada di `package main`.
- [~] Memahami `os.File`: `Read`, `Write`, `Seek`, `Sync`, dan `os.Rename`. — Read/Write/Seek sudah dipakai; Sync/Rename belum.
- [~] Memahami `testing` package: unit test, table-driven test, dan `go test`. — Sudah menulis/menjalankan test storage dasar seperti empty-tree/header; test matrix masih perlu diperluas.
- [~] Memahami cara membuat error domain sendiri. — Sentinel error sudah ada; startup masih memakai `panic` dan create database masih dapat `log.Fatal`.

**Lulus jika:** kamu dapat menjelaskan siapa pemilik setiap data utama dan bagaimana error bergerak dari storage sampai CLI.

## Milestone 1 — Kontrak perilaku dan test matrix

Sebelum implementasi, tulis contoh input dan hasil yang diharapkan.

- [x] Daftar seluruh statement yang akan didukung. — Sudah ditulis di `DESIGN.md`.
- [ ] Satu contoh sukses dan satu contoh gagal untuk setiap statement.
- [~] Kasus nama objek duplikat dan objek yang tidak ada. — Error tersedia untuk database/table/column, tetapi belum ada test matrix.
- [~] Kasus tipe nilai salah. — Validasi insert tersedia, tetapi belum ada test dan range check `int32`.
- [~] Kasus tabel kosong. — Formatter menangani hasil tanpa row, tetapi belum diuji otomatis.
- [~] Kasus text kosong, nilai negatif, dan nilai besar. — String kosong dapat direpresentasikan; angka negatif belum dikenali lexer dan nilai besar belum memiliki range check.
- [~] Kasus database ditutup lalu dibuka kembali. — Sudah diverifikasi manual bahwa row tetap terbaca setelah restart; automated restart test belum ada.
- [~] Pisahkan error syntax, semantic, constraint, dan I/O. — Sentinel sudah dikelompokkan secara nama, tetapi belum menjadi typed/category error yang konsisten.

**Lulus jika:** perilaku yang diinginkan dapat diuji tanpa perlu mengetahui detail implementasi.

## Milestone 2 — Lexer dan parser

Jangan menyentuh penyimpanan disk dahulu.

- [~] Lexer mengenali keyword, identifier, integer, string, koma, kurung, operator, dan terminator. — Semua kecuali terminator `;`; angka negatif belum; trailing whitespace dapat panic.
- [~] Lexer melaporkan lokasi token yang salah. — Unexpected character menyertakan posisi, tetapi `Token` belum menyimpan line/column/span.
- [~] Parser mendukung `CREATE DATABASE` dan `DROP DATABASE`. — `CREATE DATABASE` ada; `DROP DATABASE` belum.
- [~] Parser mendukung `CREATE TABLE` dan `DROP TABLE`. — `CREATE TABLE` ada; `DROP TABLE` belum.
- [x] Parser mendukung `INSERT`.
- [x] Parser mendukung `SELECT` dan daftar kolom.
- [ ] Parser mendukung `UPDATE`.
- [ ] Parser mendukung `DELETE`.
- [ ] Parser mendukung filter perbandingan sederhana.
- [ ] Parser mendukung `SUM`, `GROUP BY`, dan `ORDER BY`.
- [ ] Parser mendukung pembuatan index biasa dan unik.
- [x] Semua statement yang saat ini didukung diubah menjadi AST melalui interface `Statement`.
- [ ] Buat test parser (table-driven test) untuk input valid, invalid, dan tidak lengkap.

**Lulus jika:** setiap teks query dapat diubah menjadi AST yang tepat atau menjadi syntax error yang jelas.

## Milestone 3 — Storage paling sederhana

Storage dasar sekarang sudah memakai page tetap 4096 byte dan menjadi fondasi clustered tree.

- [x] Buat abstraksi `Page`, `PageID`, dan `Pager`.
- [x] Gunakan ukuran page tetap `4096` byte.
- [x] Page 0 menjadi `MetaPage` dengan magic `3DB1`, version, page size, root page ID, dan next page ID.
- [x] Implementasikan `ReadPage(pageID)` dan `WritePage(page)` dengan offset `pageID * PageSize`.
- [~] Implementasikan allocator page monotonik. — `AllocatePage()` sudah ada; perlu test khusus agar beberapa allocation sebelum write tidak menghasilkan ID sama.
- [x] Buat serialisasi/deserialisasi nilai INT, FLOAT, BOOLEAN, VARCHAR, dan NULL dalam record.
- [x] Format record v1: varlen metadata → null bitmap → flags → `NextOffset` → payload.
- [x] Gunakan `FreeStart` sebagai posisi append fisik record berikutnya di dalam page.
- [x] Dapat membaca kembali record dari disk menggunakan schema tabel.
- [x] Dapat scan seluruh record pada satu leaf melalui `FirstRecordOffset` dan `NextOffset`.
- [x] Persistence manual: row tetap identik setelah program ditutup dan dibuka kembali.
- [ ] Validasi magic number, version, page type, dan boundary record secara ketat saat read/open.
- [ ] Tambahkan automated round-trip/restart test untuk seluruh tipe data dan NULL.
- [ ] Tombstone/delete-mark belum dipakai oleh DELETE.

**Lulus jika:** sekumpulan row yang ditulis dapat dibaca kembali dengan nilai identik setelah restart.

## Milestone 3B — Target aktif: clustered primary-key B+ tree

Target ini sengaja ditempatkan setelah storage dasar karena clustered tree **adalah organisasi file tabel**, bukan file index tambahan. File `.3tbl` akan berisi meta page, internal pages, dan leaf pages. Leaf page menyimpan primary key dan seluruh encoded row.

### Kontrak versi pertama

- [x] Hanya mendukung satu `PRIMARY KEY` per tabel.
- [~] Primary key versi pertama hanya `INT`, `NOT NULL`, dan unik. — Insert sudah mengharuskan PK `int32` dan duplicate ditolak; validasi tipe/nullability sebaiknya juga ditegakkan saat `CREATE TABLE`.
- [x] Jika tabel tidak mendefinisikan primary key, tolak `CREATE TABLE` terlebih dahulu.
- [x] Identitas logis row adalah primary key, bukan offset fisik record.
- [ ] Secondary index nantinya menyimpan `(secondary_key, primary_key)`, lalu lookup dilanjutkan ke clustered tree.
- [x] Leaf sudah menyimpan full row; format internal node sudah diimplementasikan (`InternalCell`, `readInternalCells`, `rewriteInternal`).
- [ ] Semua leaf berada pada depth yang sama dan terhubung dengan `next_leaf` untuk sequential/range scan. — `next_leaf` belum ada; full scan saat ini pakai traversal rekursif dari root, bukan ikatan leaf.
- [x] Gunakan invariant separator: setiap separator adalah key terkecil pada child di sebelah kanan. — Ditegakkan di `targetPage()` dan `splitLeaf()`/`splitRootInternal()`.
- [ ] Tidak perlu merge/rebalance saat delete pada versi pertama; tandai deleted atau compact satu leaf, tetapi tree harus tetap searchable.

### Blocker / hardening sebelum split

- [ ] Perbaiki lexer agar trailing whitespace tidak mengakses index di luar string.
- [ ] Tambahkan token terminator `;` atau secara eksplisit strip terminator sebelum tokenize.
- [~] Pastikan projection parsial tidak membuat decoder/cursor desinkron. — `decodeRecord()` sekarang membaca satu record penuh; projection query masih perlu diuji.
- [x] `decodeRecord()` mengembalikan record terstruktur dan ukuran record yang dikonsumsi.
- [x] NULL bitmap dibaca berdasarkan nullable column dan digunakan saat decode.
- [x] `NextOffset` dibaca/ditulis sebagai pointer logical-next di dalam leaf.
- [ ] Validasi magic number dan file version ketika tabel dibuka.
- [~] Boundary page sudah dicek saat insert (`recordEnd <= PageSize`); batas maksimum record/VARCHAR formal masih perlu dirapikan.
- [ ] Tambahkan automated encode/decode round-trip test untuk seluruh tipe data, NULL, empty string, dan nilai batas.
- [ ] Tambahkan test insert depan, tengah, belakang, duplicate PK, dan persistence.

### Parser dan catalog primary key

- [x] Tambahkan keyword `primary` dan `key`.
- [x] Parser menerima bentuk awal: `CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR)`.
- [x] Metadata kolom menyimpan flag primary key (`ColumnDef.Primary`).
- [~] Primary key lebih dari satu sudah ditolak; nullable/non-int perlu dipastikan ditolak saat `CREATE TABLE`, bukan baru saat insert.
- [~] Catalog sudah menyimpan flag primary pada kolom; format version tabel belum ada.

### Page dan file layout

- [x] Definisikan `const PageSize = 4096` dan `type PageID uint32`.
- [x] Page 0 adalah `MetaPage` khusus: magic, version, page size, root page ID, dan next page ID.
- [x] Page 1+ menggunakan `IndexPageHeader` untuk page B+ tree.
- [x] Definisikan page type aktif: `PageTypeLeaf` dan `PageTypeInternal`.
- [x] `IndexPageHeader` berisi: page type, page ID, parent ID, level, record count, first record offset, dan free start.
- [x] Meta page **tidak** memakai `IndexPageHeader`; tidak ada common page header pada format v1.
- [x] Leaf kosong: `FirstRecordOffset = 0`, `RecordCount = 0`, dan `FreeStart = IndexPageHeaderSize`.
- [x] Record leaf dihubungkan secara logis dengan `NextOffset`; `NextOffset = 0` berarti tail/end.
- [x] Record baru ditulis secara fisik pada `FreeStart`; urutan fisik tidak harus sama dengan urutan primary key.
- [x] Versi sekarang sengaja tidak memakai slot directory dan tidak memakai INFIMUM/SUPREMUM.
- [ ] Tentukan format internal page sederhana: separator key + child page ID.
- [x] Implementasikan `ReadPage(pageID)` dan `WritePage(page)` menggunakan offset `int64(pageID) * PageSize`.
- [~] Implementasikan allocator page monotonik; tambahkan test multi-allocation dan persist `NextPageID` bila allocator mulai bergantung pada meta.
- [ ] Tentukan mekanisme scan antar-leaf setelah split sebelum full table scan multi-page.

### Operasi B+ tree secara bertahap

- [x] Buat tabel baru dengan root berupa satu leaf page kosong.
- [ ] Optimalkan pencarian key di leaf. — Saat ini pencarian posisi insert masih linear melalui `NextOffset`; binary search belum relevan sampai ada struktur offset/index tambahan.
- [x] Implementasikan traversal root → internal → leaf untuk `Find(pk)` setelah internal page tersedia. — `targetPage()` sudah rekursif.
- [x] Implementasikan insert terurut ke leaf selama page masih cukup.
- [x] Tolak duplicate primary key sebelum perubahan dipublikasikan.
- [x] Implementasikan leaf split, perbarui sibling link, lalu naikkan separator ke parent. — `splitLeaf()` memecah leaf dan memanggil `insertInternalCell()`; `next_leaf` sibling link belum diikat.
- [x] Implementasikan pembuatan root internal baru ketika root leaf pecah. — `splitRootLeaf()`.
- [x] Implementasikan insert separator ke internal page. — `insertInternalCell()`.
- [x] Implementasikan internal split dan recursive parent split. — `splitRootInternal()` memecah root internal; **masih ada bug kehilangan data pada insert banyak record** (lihat status audit).
- [x] Perbarui `root_page_id` di meta page ketika root berubah. — Dilakukan di `splitRootLeaf()` dan `splitRootInternal()`.
- [x] Full scan single-leaf sudah berfungsi melalui `FirstRecordOffset` → `NextOffset`; scan multi-leaf sudah ada via `scanTree()`.
- [ ] Implementasikan `SELECT ... WHERE pk = value` melalui clustered lookup.
- [ ] Implementasikan `UPDATE` non-PK; bila ukuran row tidak lagi muat, lakukan delete + reinsert.
- [ ] Implementasikan perubahan PK sebagai delete old key + insert new key.
- [ ] Implementasikan delete sederhana tanpa merge terlebih dahulu.

### Test wajib sebelum integrasi SQL penuh

- [x] Insert key berurutan sudah diuji pada satu leaf (`TestInsertSingleLeafOrdered`); split sudah diuji (`TestInsertSplitRootLeaf`, `TestInsertSplitLeafSameRoot`).
- [ ] Insert key menurun: `100,99,98,...`.
- [x] Insert key tidak berurutan sudah diuji (`TestInsertSplitRootLeaf`/`TestInsertSplitLeafSameRoot`); automated random-seed test belum.
- [x] Duplicate key sudah ditolak pada insert; perlu automated test bahwa bytes/tree tidak berubah.
- [~] Persistence setelah reopen sudah terbukti manual melalui `SELECT`; automated restart test belum ada.
- [x] Full scan pada single-leaf menghasilkan primary key terurut melalui chain `NextOffset`.
- [ ] Semua leaf memiliki depth yang sama. — Belum divalidasi; inilah yang dicurigai menyebabkan bug kehilangan data (`TestMultiLevelTreeInsertAndSelect` gagal: 5000 → 672).
- [ ] Parent pointer, child pointer, separator, dan sibling link valid setelah setiap split.
- [x] Root split lebih dari sekali sudah diuji (`TestSplitRootInternal` dengan 2045 insert → level 2).
- [ ] Fuzz urutan insert dan bandingkan hasilnya dengan `map[int32]Row` + sorted keys.

**Lulus jika:** setelah ribuan insert acak dan beberapa kali restart, lookup setiap primary key benar, full scan selalu terurut, duplicate key tidak mengubah tree, dan validator tidak menemukan pointer/separator yang rusak.

### Bug yang harus diselesaikan (blocker)

- [ ] **Kehilangan data pada tree multi-level.** `TestMultiLevelTreeInsertAndSelect` gagal: insert 5000 record (value ~1KB) hanya menghasilkan 672 record saat `SELECT`. Diduga:
  - separator / parent pointer rusak saat `splitRootInternal()` dipanggil untuk internal page yang bukan root, atau
  - `scanTree()` melewatkan subtree karena traversal tidak mengikuti seluruh child, atau
  - record hilang saat rewrite internal/leaf pada split beruntun.
  - Perlu validator tree yang mengecek depth seragam, parent/child konsisten, dan jumlah record == jumlah insert.

## Milestone 4 — Catalog dan DDL

Catalog adalah sumber kebenaran metadata, bukan isi direktori yang ditebak-tebak.

- [x] Catalog mencatat database.
- [x] Catalog mencatat tabel dan skemanya.
- [x] Catalog mencatat urutan, nama, tipe, nullable, dan default setiap kolom.
- [ ] Catalog mencatat index, kolom target, jenis biasa/unik, dan file index.
- [~] Implementasikan create database. — Sudah ada, tetapi path masih dikonkatenasi langsung, mkdir masih hard-coded, dan error dapat menghentikan proses.
- [ ] Implementasikan drop database dengan pemeriksaan target yang ketat.
- [~] Implementasikan create table. — Meta page + root leaf + catalog sudah dibuat; operasi belum atomic dan belum rollback jika catalog save gagal.
- [ ] Implementasikan drop table beserta metadata index miliknya.
- [ ] Perubahan metadata dilakukan secara aman (write ke file sementara lalu `os.Rename`) agar file setengah tertulis tidak dianggap valid.
- [~] Uji catalog setelah restart. — `LoadCatalog` ada; automated restart test belum.

**Lulus jika:** create/drop menghasilkan keadaan disk dan metadata yang konsisten, termasuk setelah aplikasi dibuka ulang.

## Milestone 5 — Binder dan validasi semantik

- [~] Pastikan database aktif tersedia. — Dicek oleh insert/select, tetapi belum dipisahkan menjadi binder.
- [x] Resolusi nama tabel ke metadata catalog.
- [~] Resolusi nama kolom ke posisi dan tipe. — Dilakukan di executor, belum menghasilkan bound AST/column index.
- [ ] Tolak kolom duplikat pada definisi tabel.
- [x] Tolak referensi kolom yang tidak ada untuk insert/select.
- [x] Periksa jumlah dan tipe nilai insert.
- [ ] Periksa kecocokan tipe pada filter dan assignment update.
- [ ] Pastikan `SUM` hanya menerima tipe yang didukung.
- [ ] Terapkan aturan validitas kolom pada `GROUP BY`.
- [ ] Tolak index pada kolom yang tidak ada.

**Lulus jika:** executor hanya menerima statement yang sudah sah secara struktur dan tipe.

## Milestone 6 — CRUD tanpa index

Kerjakan satu demi satu; jangan menggabungkan semuanya sekaligus.

### INSERT

- [~] Bangun row sesuai urutan schema. — Encoder mengurutkan berdasarkan schema, tetapi default value belum diterapkan.
- [~] Validasi tipe dan nullability. — Validasi dasar ada; duplicate insert column dan integer overflow belum ditolak.
- [x] Tulis row ke clustered leaf: append fisik di `FreeStart`, urutan logis dijaga melalui `FirstRecordOffset`/`NextOffset`.
- [ ] Kembalikan jumlah row yang ditambahkan.

### SELECT

- [x] Sequential scan single-leaf melalui chain record sudah berfungsi untuk seluruh kolom.
- [ ] Filter row.
- [x] Projection semua kolom.
- [~] Projection kolom tertentu dan urutan kolom hasil. — Decoder record sudah membaca payload penuh; perilaku projection/urutan query masih perlu diuji dan dirapikan.
- [x] Format nama kolom dan nilai hasil.

### UPDATE

- [ ] Sequential scan dan filter target.
- [ ] Hitung nilai row baru.
- [ ] Validasi row baru sebelum penulisan.
- [ ] Simpan perubahan.
- [ ] Kembalikan jumlah row yang berubah.

### DELETE

- [ ] Sequential scan dan filter target.
- [ ] Tandai record terhapus.
- [ ] Pastikan scan berikutnya mengabaikan tombstone.
- [ ] Kembalikan jumlah row yang terhapus.

**Lulus jika:** seluruh CRUD benar untuk tabel kosong, satu row, banyak row, tanpa filter, dan dengan filter.

## Milestone 7 — ORDER BY

- [ ] Tentukan aturan ascending dan descending.
- [ ] Tentukan posisi `NULL`.
- [ ] Ambil row hasil filter sebelum sorting.
- [ ] Sort menggunakan satu kolom dahulu (`sort.Slice` atau `slices.SortFunc`).
- [ ] Pastikan hasil stabil dan deterministik untuk nilai yang sama (pakai `sort.SliceStable` bila perlu).
- [ ] Uji integer, text, nilai sama, tabel kosong, dan hasil satu row.

**Lulus jika:** hasil terurut benar dan tidak mengubah data asli di storage.

## Milestone 8 — SUM dan GROUP BY

Kerjakan `SUM` tanpa grouping lebih dahulu.

- [ ] `SUM` pada tabel kosong memiliki perilaku yang ditentukan.
- [ ] `SUM` mengabaikan atau memproses `NULL` sesuai aturan yang ditulis.
- [ ] Deteksi overflow integer (cek batas `int64` sebelum/after penjumlahan).
- [ ] Setelah benar, tambahkan `GROUP BY` satu kolom.
- [ ] Gunakan key grup (misal `map[string]*Accumulator`) untuk mengumpulkan accumulator.
- [ ] Projection hanya menerima group key atau hasil agregasi.
- [ ] Terapkan `ORDER BY` pada hasil agregasi, bukan row mentah.
- [ ] Uji satu grup, banyak grup, grup bernilai null, dan hasil tanpa row.

**Lulus jika:** hasil agregasi cocok dengan perhitungan manual pada dataset kecil yang diketahui.

## Milestone 9 — Secondary index satu kolom

Kerjakan setelah clustered primary-key B+ tree stabil. Secondary index disimpan pada file index terpisah dan value-nya adalah primary key row, bukan alamat fisik page/slot.

### Kontrak index

- [ ] Key berasal dari tepat satu kolom.
- [ ] Value menyimpan primary key sebagai identitas logis row; lookup row dilanjutkan ke clustered tree.
- [ ] Index biasa dapat menyimpan banyak record untuk key yang sama.
- [ ] Index unik menolak key duplikat.
- [ ] Tentukan aturan uniqueness untuk `NULL`.
- [ ] Index tidak menyimpan record yang sudah terhapus.

### Siklus hidup

- [ ] Create index memindai seluruh tabel dan membangun entry.
- [ ] Create unique index gagal tanpa meninggalkan index parsial bila data lama duplikat.
- [ ] Insert menambah entry index.
- [ ] Update menghapus key lama lalu menambah key baru jika kolom terindex berubah.
- [ ] Delete menghapus entry index.
- [ ] Drop table menghapus seluruh index tabel.
- [ ] Catalog dan file index selalu konsisten.
- [ ] Rebuild index dapat dibuat dari table scan.

### Pemakaian oleh planner

- [ ] Table scan tetap menjadi jalur yang selalu benar.
- [ ] Gunakan index untuk equality filter pada kolom yang cocok.
- [ ] Ambil primary key dari secondary index, lookup full row di clustered tree, lalu verifikasi filter.
- [ ] Jelaskan rencana yang dipilih melalui mode debug atau `EXPLAIN` sederhana.
- [ ] Bandingkan hasil index scan dengan table scan untuk query yang sama.

**Lulus jika:** untuk dataset yang sama, table scan dan index scan selalu menghasilkan row identik.

## Milestone 10 — Atomicity dan pemulihan minimal

Fitur ini penting sebelum database disebut cukup aman, walau belum memiliki transaksi lengkap.

- [ ] Identifikasi setiap operasi yang mengubah lebih dari satu file.
- [ ] Jangan publikasikan metadata sebelum file data/index siap.
- [ ] Gunakan file sementara dan `os.Rename` (rename aman, atomik di level filesystem) untuk perubahan catalog.
- [ ] Tentukan perilaku bila proses berhenti di tengah create/drop.
- [ ] Tambahkan pemeriksaan integritas saat startup.
- [ ] Sediakan cara rebuild index dari tabel.
- [ ] Dokumentasikan bahwa durability penuh belum dijamin tanpa WAL dan `fsync` (`File.Sync()`) yang benar.
- [ ] Jadikan write-ahead log sebagai milestone lanjutan, bukan syarat CRUD awal.

**Lulus jika:** simulasi kegagalan di titik penting tidak membuat catalog menunjuk ke file yang tidak valid tanpa terdeteksi.

## Milestone 11 — Integrasi dan kualitas

- [ ] Satu skenario end-to-end mencakup create database sampai drop database.
- [ ] Test restart setelah setiap jenis mutasi.
- [ ] Test data dengan duplicate key untuk index biasa.
- [ ] Test penolakan duplicate key untuk unique index.
- [ ] Property test serialisasi/deserialisasi (bisa pakai `testing/quick` atau library fuzzing bawaan `go test -fuzz`).
- [ ] Property test bahwa hasil indexed lookup sama dengan table scan.
- [ ] Fuzz lexer/parser dengan `go test -fuzz` (native fuzzing Go).
- [ ] Pesan error menyertakan konteks dan tidak membocorkan panic ke pengguna (pakai `recover` di boundary CLI kalau perlu, tapi utamakan error return).
- [ ] Dokumentasikan format file dan kompatibilitas versi.
- [ ] Ukur waktu scan dan lookup setelah kebenaran terjamin (`go test -bench`).

## Urutan kerja terdekat

Kerjakan dalam urutan ini agar konsep tree bertambah satu lapis pada satu waktu:

1. [ ] Tambahkan automated test insert single-leaf: kosong, depan, tengah, belakang, duplicate, NULL/NOT NULL, dan reopen.
2. [ ] Pastikan root leaf memakai invariant `ParentID = InvalidPageID` dan `Level = 0`.
3. [ ] Tegakkan kontrak PK v1 saat `CREATE TABLE`: tepat satu PK, `INT`, `NOT NULL`.
4. [ ] Tambahkan test yang sengaja memenuhi leaf sampai `recordEnd > PageSize`.
5. [ ] Implementasikan **leaf split** menjadi leaf kiri + leaf kanan.
6. [ ] Ketika root leaf split, buat **root internal baru** dan update `MetaPage.RootPageID`.
7. [ ] Tentukan format entry internal dan implementasikan traversal `root → ... → leaf`.
8. [ ] Implementasikan insert separator ke internal page.
9. [ ] Implementasikan internal split dan recursive parent split.
10. [ ] Tentukan mekanisme scan antar-leaf dan perluas `SELECT *` menjadi full scan multi-leaf.
11. [ ] Setelah tree multi-level stabil, tambahkan `SELECT ... WHERE pk = value`.

Jangan mulai secondary index, planner, buffer pool, atau WAL sebelum leaf split, internal traversal, dan recursive root handling stabil.

## Urutan demo akhir

- [ ] Membuat database dan membukanya.
- [ ] Membuat tabel dengan kolom integer dan text.
- [ ] Menambahkan beberapa row, termasuk nilai grup yang sama.
- [ ] Memilih seluruh row.
- [ ] Memilih kolom tertentu.
- [ ] Memfilter row.
- [ ] Mengurutkan hasil.
- [ ] Menghitung `SUM`.
- [ ] Mengelompokkan dan menghitung `SUM` per grup.
- [ ] Mengubah row lalu memverifikasi hasil.
- [ ] Menghapus row lalu memverifikasi hasil.
- [ ] Membuat index biasa pada satu kolom.
- [ ] Membuktikan duplicate key diperbolehkan.
- [ ] Membuat unique index pada kolom lain.
- [ ] Membuktikan duplicate key ditolak tanpa merusak data.
- [ ] Menutup dan membuka program, lalu membuktikan data serta index tetap benar.
- [ ] Menghapus index/tabel sesuai sintaks yang dipilih.
- [ ] Menghapus database.

## Cara belajar pada setiap checklist

Untuk setiap item:

1. Tulis prediksi alur data dengan kata-katamu sendiri.
2. Tulis test atau contoh perilaku yang diharapkan (`go test`).
3. Implementasikan bagian terkecil.
4. Jalankan test.
5. Sengaja buat satu input gagal dan baca error-nya.
6. Jelaskan kembali mengapa implementasimu benar.
7. Commit hanya setelah satu konsep benar-benar selesai.

## Pertanyaan wajib sebelum naik milestone

- Data ini dimiliki komponen mana?
- Apa invariant yang harus selalu benar?
- Apa yang terjadi pada tabel kosong?
- Apa yang terjadi jika input salah?
- Apa yang terjadi jika proses berhenti di tengah penulisan?
- Bagaimana hasil ini diuji tanpa melihat implementasinya?
- Apakah optimasi ini mengubah hasil atau hanya cara mendapatkannya?

## Invariant terpenting

- Catalog hanya menunjuk objek yang valid.
- Setiap row mengikuti schema tabelnya.
- Identitas record tidak ambigu.
- Record terhapus tidak muncul pada query.
- Setiap entry index menunjuk record yang masih valid.
- Semua record aktif memiliki entry pada setiap index yang relevan.
- Unique index memiliki paling banyak satu record untuk setiap key yang dianggap setara.
- Index scan dan table scan memberi hasil logis yang sama.
- Error tidak boleh meninggalkan perubahan setengah jadi yang dianggap sukses.
- `MetaPage.RootPageID` selalu menunjuk root B+ tree yang aktif.
- Leaf root pada tree satu-level memiliki `Level = 0` dan `ParentID = InvalidPageID`.
- `FirstRecordOffset = 0` berarti leaf kosong.
- `Record.NextOffset = 0` berarti record tersebut tail/end dalam urutan logis leaf.
- `FreeStart` menunjuk byte pertama yang boleh dipakai untuk append fisik record baru.
- Urutan record yang dicapai dari `FirstRecordOffset` melalui `NextOffset` harus selalu ascending berdasarkan primary key.
- Posisi fisik record di page boleh berbeda dari urutan logis primary key.

## Setelah versi pertama selesai

Jangan mulai bagian ini sebelum seluruh demo akhir lulus.

- [ ] Buffer pool dan page replacement.
- [ ] Optimasi lanjutan B+ tree: merge/rebalance delete, bulk loading, prefix compression, dan page compaction.
- [ ] Free-page list dan vacuum/compaction.
- [ ] Write-ahead log dan crash recovery.
- [ ] Transaction begin/commit/rollback.
- [ ] Isolation dan concurrency control (di sinilah goroutine + mutex/channel mulai relevan).
- [ ] Cost-based query planning.
- [ ] Join.
- [ ] Multi-column index.
- [ ] Foreign key dan constraint tambahan.
- [ ] Network protocol client/server (bisa mulai dari `net` package bawaan Go).
