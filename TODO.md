# Roadmap Belajar Go: Membuat RDBMS Mini

Dokumen ini adalah peta belajar dan checklist. Semua implementasi Go harus ditulis sendiri oleh pemilik proyek.

## Status audit — 7 Agustus 2026

Legenda:

- `[x]` selesai dan sudah terlihat pada implementasi;
- `[~]` sebagian sudah ada, tetapi belum memenuhi seluruh kontrak atau masih memiliki bug;
- `[ ]` belum dikerjakan.

### Ringkasan kondisi saat ini

- Sudah ada REPL, config loader, lexer, parser AST, catalog JSON, `CREATE DATABASE`, `CREATE TABLE`, `INSERT`, dan `SELECT` sequential scan.
- Format row awal sudah memiliki status byte, panjang null bitmap, null bitmap, dan payload bertipe.
- Source Go lolos compile check setelah nama file dinormalisasi dan directive Go disesuaikan dengan toolchain audit; project belum memiliki test source resmi.
- Storage masih append-only stream; belum ada page 4096 byte, page ID, slot directory, root page, atau free-page management.
- Target berikutnya dipindahkan ke **clustered primary-key B+ tree**: file tabel menjadi tree utama dan leaf menyimpan full row.
- Sebelum masuk tree split, perbaiki decoder projection, trailing-whitespace panic pada lexer, validasi table header, dan batas maksimum row.

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
- [~] Tentukan batas ukuran text dan ukuran page. — Sudah dipilih page 4096 byte dan row sekitar 3500 byte di `DESIGN.md`, tetapi belum ditegakkan oleh storage; kode masih menerima varchar hingga 16 MiB.
- [~] Tentukan format direktori dan nama file di disk. — Sudah ada `DataDirectory`, `CatalogPath`, dan ekstensi `.3tbl`; path creation masih perlu dirapikan dengan `filepath.Join`.
- [~] Tentukan format metadata katalog dan nomor versinya. — Catalog JSON sudah ada, tetapi belum memiliki `catalog_version` dan metadata primary/index.
- [~] Tentukan format record: header, null bitmap, dan payload. — Format awal sudah ada, tetapi belum memiliki total row length, slot, dan boundary page.
- [ ] Tentukan identitas stabil sebuah record. — Untuk clustered tree, gunakan **primary key sebagai identitas logis**; `PageID + slot` hanya lokasi fisik sementara karena row dapat berpindah saat split.
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
- [ ] Memahami `testing` package: unit test, table-driven test, dan `go test`.
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
- [~] Kasus database ditutup lalu dibuka kembali. — Catalog/data dibaca dari disk, tetapi belum ada restart test.
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

Mulai dari record append-only agar konsep disk mudah terlihat.

- [ ] Buat abstraksi file dan page (struct `Page`, `File` dengan method `os.File` di baliknya).
- [~] Tentukan header file dan magic number. — Header 15 byte dengan magic `3DB1` dan version 1 sudah ditulis, tetapi belum divalidasi saat open.
- [ ] Nomori setiap page.
- [~] Buat serialisasi dan deserialisasi nilai. — Int/float/bool/varchar/null sudah ada; decoder masih memiliki bug projection
- [~] Buat format row/record. — Ada status byte + bitmap length + null bitmap + payload, tetapi belum ada row length dan slot directory.
- [ ] Buat slot directory agar record dapat ditemukan lewat identitas stabil.
- [x] Dapat menambah record secara append-only.
- [ ] Dapat membaca ulang record berdasarkan identitasnya.
- [~] Dapat melakukan sequential scan seluruh record. — `SELECT *` tersedia; projection sebagian dapat membuat cursor desinkron karena payload kolom yang tidak dipilih tidak dikonsumsi.
- [~] Tandai record terhapus dengan tombstone terlebih dahulu. — Status byte sudah ditulis, tetapi belum dibaca/dipakai oleh DELETE/scan.
- [ ] Pastikan file yang rusak atau versi yang salah ditolak dengan error (bukan panic).
- [ ] Uji persistence setelah proses ditutup dan dibuka lagi.

**Lulus jika:** sekumpulan row yang ditulis dapat dibaca kembali dengan nilai identik setelah restart.

## Milestone 3B — Target aktif: clustered primary-key B+ tree

Target ini sengaja ditempatkan setelah storage dasar karena clustered tree **adalah organisasi file tabel**, bukan file index tambahan. File `.3tbl` akan berisi meta page, internal pages, dan leaf pages. Leaf page menyimpan primary key dan seluruh encoded row.

### Kontrak versi pertama

- [ ] Hanya mendukung satu `PRIMARY KEY` per tabel.
- [ ] Primary key versi pertama hanya `INT`, `NOT NULL`, dan unik.
- [ ] Jika tabel tidak mendefinisikan primary key, tolak `CREATE TABLE` terlebih dahulu; hidden row ID dapat ditambahkan setelah versi dasar stabil.
- [ ] Identitas logis row adalah primary key, bukan `PageID + slot`, karena split dapat memindahkan row.
- [ ] Secondary index nantinya menyimpan `(secondary_key, primary_key)`, lalu lookup dilanjutkan ke clustered tree.
- [ ] Leaf menyimpan full row; internal node hanya menyimpan separator key dan child page ID.
- [ ] Semua leaf berada pada depth yang sama dan terhubung dengan `next_leaf` untuk sequential/range scan.
- [ ] Gunakan invariant separator: setiap separator adalah key terkecil pada child di sebelah kanan.
- [ ] Tidak perlu merge/rebalance saat delete pada versi pertama; tandai deleted atau compact satu leaf, tetapi tree harus tetap searchable.

### Blocker yang harus diperbaiki dahulu

- [ ] Perbaiki lexer agar trailing whitespace tidak mengakses index di luar string.
- [ ] Tambahkan token terminator `;` atau secara eksplisit strip terminator sebelum tokenize.
- [ ] Perbaiki `decodeRow`: seluruh payload kolom harus selalu dikonsumsi, baru projection dilakukan.
- [ ] Ganti error bitmap length mismatch yang saat ini dapat mengembalikan `nil` error menjadi `ErrCorruptTableFile`.
- [ ] Perbaiki decoded boolean agar `Value.Type == BooleanType`.
- [ ] Validasi magic number dan file version ketika tabel dibuka.
- [ ] Samakan batas row dengan desain page: encoded row + slot + cell header harus muat dalam satu leaf page.
- [ ] Tambahkan test encode/decode round-trip sebelum format lama diganti.

### Parser dan catalog primary key

- [ ] Tambahkan keyword `primary` dan `key`.
- [ ] Parser menerima bentuk awal: `CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR)`.
- [ ] Tambahkan `PrimaryKey bool` ke `ColumnDef`, atau `PrimaryKeyColumn string` ke `TableDef`.
- [ ] Tolak primary key lebih dari satu, nullable primary key, dan primary key non-int pada versi pertama.
- [ ] Catalog menyimpan primary-key column dan format version tabel.

### Page dan file layout

- [ ] Definisikan `const PageSize = 4096` dan `type PageID uint32`.
- [ ] Page 0 menjadi meta page dengan minimal: magic, version, page size, root page ID, first leaf page ID, next page ID, dan free-list head.
- [ ] Definisikan page type: `MetaPage`, `InternalPage`, `LeafPage`, dan `FreePage`.
- [ ] Definisikan common page header: page type, page ID, parent page ID, cell count, free-space start, dan free-space end.
- [ ] Leaf header memiliki `prev_leaf` dan `next_leaf`.
- [ ] Leaf menggunakan slotted-page layout karena encoded row berukuran variabel.
- [ ] Internal page memakai `first_child_page_id` lalu cell berulang `{separator_key int32, right_child_page_id}`.
- [ ] Implementasikan `ReadPage(pageID)` dan `WritePage(pageID)` menggunakan offset `int64(pageID) * PageSize`.
- [ ] Implementasikan allocator page monotonik dahulu; free list dapat tetap kosong pada tahap awal.

### Operasi B+ tree secara bertahap

- [ ] Buat tabel baru dengan root berupa satu leaf page kosong.
- [ ] Implementasikan binary search key di dalam leaf.
- [ ] Implementasikan traversal root → internal → leaf untuk `Find(pk)`.
- [ ] Implementasikan insert terurut ke leaf selama page masih cukup.
- [ ] Tolak duplicate primary key sebelum perubahan dipublikasikan.
- [ ] Implementasikan leaf split, perbarui sibling link, lalu naikkan separator ke parent.
- [ ] Implementasikan pembuatan root internal baru ketika root leaf pecah.
- [ ] Implementasikan insert separator ke internal page.
- [ ] Implementasikan internal split dan recursive parent split.
- [ ] Perbarui `root_page_id` di meta page ketika root berubah.
- [ ] Implementasikan full scan mulai dari left-most leaf lalu mengikuti `next_leaf`.
- [ ] Implementasikan `SELECT ... WHERE pk = value` melalui clustered lookup.
- [ ] Implementasikan `UPDATE` non-PK; bila ukuran row tidak lagi muat, lakukan delete + reinsert.
- [ ] Implementasikan perubahan PK sebagai delete old key + insert new key.
- [ ] Implementasikan delete sederhana tanpa merge terlebih dahulu.

### Test wajib sebelum integrasi SQL penuh

- [ ] Insert key berurutan: `1,2,3,...` sampai terjadi beberapa split.
- [ ] Insert key menurun: `100,99,98,...`.
- [ ] Insert key acak dengan seed tetap.
- [ ] Duplicate key selalu ditolak dan tree tetap identik.
- [ ] Setiap key yang ditulis dapat ditemukan setelah reopen file.
- [ ] Full scan menghasilkan primary key terurut.
- [ ] Semua leaf memiliki depth yang sama.
- [ ] Parent pointer, child pointer, separator, dan sibling link valid setelah setiap split.
- [ ] Root split lebih dari sekali.
- [ ] Fuzz urutan insert dan bandingkan hasilnya dengan `map[int32]Row` + sorted keys.

**Lulus jika:** setelah ribuan insert acak dan beberapa kali restart, lookup setiap primary key benar, full scan selalu terurut, duplicate key tidak mengubah tree, dan validator tidak menemukan pointer/separator yang rusak.

## Milestone 4 — Catalog dan DDL

Catalog adalah sumber kebenaran metadata, bukan isi direktori yang ditebak-tebak.

- [x] Catalog mencatat database.
- [x] Catalog mencatat tabel dan skemanya.
- [x] Catalog mencatat urutan, nama, tipe, nullable, dan default setiap kolom.
- [ ] Catalog mencatat index, kolom target, jenis biasa/unik, dan file index.
- [~] Implementasikan create database. — Sudah ada, tetapi path masih dikonkatenasi langsung, mkdir masih hard-coded, dan error dapat menghentikan proses.
- [ ] Implementasikan drop database dengan pemeriksaan target yang ketat.
- [~] Implementasikan create table. — File/header dan catalog dibuat, tetapi belum atomic dan belum rollback jika catalog save gagal.
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
- [x] Tulis row ke storage append-only.
- [ ] Kembalikan jumlah row yang ditambahkan.

### SELECT

- [~] Sequential scan tabel. — Berfungsi untuk seluruh kolom; projection parsial masih merusak posisi baca.
- [ ] Filter row.
- [x] Projection semua kolom.
- [~] Projection kolom tertentu dan urutan kolom hasil. — Ada validasi, tetapi decoder tidak skip payload dan output mengikuti urutan schema, bukan urutan query.
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

Kerjakan dalam urutan ini agar tidak mencampur terlalu banyak konsep:

1. [ ] Tambah test lexer trailing whitespace dan perbaiki panic.
2. [ ] Tambah round-trip test row untuk seluruh tipe dan NULL.
3. [ ] Perbaiki decoder agar selalu membaca seluruh row sebelum projection.
4. [ ] Tambah sintaks `PRIMARY KEY` dan metadata catalog.
5. [ ] Buat package/storage types untuk `PageID`, page header, meta page, read page, dan write page.
6. [ ] Buat satu leaf root tanpa split dan integrasikan `Find` + `Insert` langsung melalui API storage.
7. [ ] Implementasikan leaf split dan root split.
8. [ ] Implementasikan recursive internal split.
9. [ ] Ganti `Executor.Insert` append-only menjadi insert ke clustered tree.
10. [ ] Ganti sequential `SELECT` menjadi leaf-chain scan; tambahkan PK lookup setelah filter AST tersedia.

Jangan mulai secondary index, planner, buffer pool, atau WAL sebelum langkah 1–8 lulus test.

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
