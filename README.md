# Digiflazz API integrasi OtomaX (Go 1.24)

Layanan HTTP untuk menerima transaksi postpaid OtomaX (GET) dan meneruskan proses ke Digiflazz (POST), dengan idempotensi `ref_id`, cache SQLite, serta update status ke SQL Server OtomaX.

## Fitur
- Endpoint GET `/api/otomax` untuk `action=inquiry|payment`
- **Endpoint POST `/api/seller/topup`** — menerima request dari DigiFlazz Seller API (topup), validasi sign & price, forward ke OtomaX via InsertInbox, response format DigiFlazz
- Validasi IP whitelist dan signature HMAC (`sign = HMAC-SHA256(secret, ref_id+ts)`)
- Integrasi Digiflazz (POST JSON) untuk inquiry dan payment
- Idempotensi dan cache transaksi di SQLite (UNIQUE `ref_id`)
- Update status pembayaran ke SQL Server OtomaX
- Health check: `GET /healthz`

## Teknologi
- Go 1.24
- SQLite 3 (local cache/idempotensi)
- SQL Server (OtomaX)

## Konfigurasi
Salin `.env.example` ke `.env` dan isi variabel penting.

## Jalankan
```bash
go run ./cmd/server
```

## Test dengan Digiflazz Test Case
Rujukan: [Test Case Digiflazz](https://developer.digiflazz.com/api/buyer/test-case/)

Jalankan server, lalu di terminal lain:
```bash
go run ./cmd/tester
```
Secara default akan memanggil beberapa test case HP (sukses/gagal/pending) untuk inquiry dan payment ke endpoint lokal.

## Sejarah Perubahan

### [Unreleased]

#### Ditambahkan
- (Tidak ada)

---

### 2026-02-20 — DigiFlazz Seller API (incoming topup) & OtomaX InsertInbox

#### Ditambahkan
- **Endpoint POST `/api/seller/topup`** — menerima request dari DigiFlazz Seller (topup):
  - Validasi **sign** = `md5(username + apiKey + ref_id)`
  - **Idempotensi**: `ref_id` sama mengembalikan response yang sudah disimpan (SQLite)
  - **Validasi price**: harga dari DigiFlazz tidak boleh lebih murah dari `harga_jual` produk (sumber: OtomaX **QueryProduk** atau MSSQL tabel produk)
  - **Forward ke OtomaX** via **InsertInbox**; format pesan dari .env (**OTOMAX_INSERTINBOX_PESAN_FORMAT**) dengan placeholder `{{ref_id}}`, `{{hp}}`, `{{pulsa_code}}`, `{{price}}`, `{{username}}`, `{{commands}}`
  - Response ke DigiFlazz dalam format `{ "data": { ref_id, status, code, hp, price, message, balance, tr_id, rc, sn } }` (pending: status "0", rc "39")
- **Config & .env**:
  - `OTOMAX_INSERTINBOX_PESAN_FORMAT` — template format pesan InsertInbox (default: `{{pulsa_code}}.{{hp}}.{{price}}.{{ref_id}}`). Kode reseller untuk InsertInbox = **username** dari request DigiFlazz.
- **OtomaX client** (`internal/otomax`): **InsertInbox**, **QueryProduk** (GetProduct untuk pembandingan harga); helper **BuildPesanFromTemplate** untuk parsing request DigiFlazz ke format pesan
- **Domain**: action `seller_topup` untuk transaksi incoming DigiFlazz di SQLite

#### Dokumentasi
- Rule `.cursor/rules/project-rule-incoming.mdc`: alur request DigiFlazz, OtomaX API (GetTrx, GetSaldoRs, InsertInbox, QueryProduk), format pesan di .env, callback, response ke DigiFlazz

---

### Sebelumnya

#### Ditambahkan
- Query parameter `counter` untuk endpoint prepaid (`/api/otomax/prepaid`)
  - Jika request memiliki parameter `counter=1`, aplikasi akan meneruskan ke Digiflazz dengan `ref_id=C1-[ref_id]`
  - Format: `C{counter}-{ref_id}` (contoh: `counter=1` dan `ref_id=ABC123` → Digiflazz menerima `C1-ABC123`)
  - Jika `counter` tidak ada atau `counter=0`, `ref_id` digunakan tanpa modifikasi


