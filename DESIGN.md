# Design

## Sintaks SQL

basicly seperti sql biasa di posgresql dan sqlite, tapi versi pendek

- CREATE DATABASE <db_name>;
- DROP DATABASE <db_name>;
- CREATE TABLE <table_name>;
- DROP TABLE <table_name>;
- IF EXISTS // used to DROP DATABASE or DROP TABLE or DROP INDEX
- SELECT .. FROM .. ORDER BY
- SELECT SUM ..
- INSERT INTO <table_name> VALUES (value1, value2)
- UPDATE <table_name> SET (column = new_value) WHERE column = value
- DELETE FROM <table_name> where column = value
- AND & OR // keyword for WHERE

## Aturan nama db, table , kolom dan index

untuk db dan table, nama hanya boleh alphanumeric
untuk kolom dan index hanya boleh alpha(a-z)
bersifat case insensitive untuk dbname dan table dan string

## NULL handling

basicly seperti postgresql dan sqlite

- Adding anything to null gives null | YES
- Multiplying null by zero gives null | YES
- nulls are distinct in a UNIQUE column | YES
- nulls are distinct in SELECT DISTINCT | NO
- nulls are distinct in a UNION | NO
- "CASE WHEN null THEN 1 ELSE 0 END" is 0? | YES
- "null OR true" is true | YES
- "not (null AND false)" is true| YES

## Ukuran TEXT dan ukuran PAGE

- Page size: pilih 4096 byte — samain dengan default SQLite modern, gampang dijelasin, dan biasanya align dengan OS page size.
- Batas text: kalau belum mau implement overflow page, batasi text supaya 1 row selalu muat dalam 1 page (misal max ~3500 byte per row, nyisain ruang buat header+slot). Ini simplifikasi valid untuk versi pertama

## FILE format

File format .3tbl (my name is 'Tri Adi', Tri (three) means number three (3))
