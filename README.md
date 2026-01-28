# Digiflazz API integrasi OtomaX (Go 1.24)

Layanan HTTP untuk menerima transaksi postpaid OtomaX (GET) dan meneruskan proses ke Digiflazz (POST), dengan idempotensi `ref_id`, cache SQLite, serta update status ke SQL Server OtomaX.

## Fitur
- Endpoint GET `/api/otomax` untuk `action=inquiry|payment`
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
- Query parameter `counter` untuk endpoint prepaid (`/api/otomax/prepaid`)
  - Jika request memiliki parameter `counter=1`, aplikasi akan meneruskan ke Digiflazz dengan `ref_id=C1-[ref_id]`
  - Format: `C{counter}-{ref_id}` (contoh: `counter=1` dan `ref_id=ABC123` → Digiflazz menerima `C1-ABC123`)
  - Jika `counter` tidak ada atau `counter=0`, `ref_id` digunakan tanpa modifikasi


