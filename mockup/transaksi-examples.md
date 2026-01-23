# Mockup Transaksi - Contoh Response

## Inquiry Sukses (Pascabayar - PLN)

**Request:**
```
GET /api/otomax?action=inquiry&ref_id=53
```

**Response:**
```json
{
  "data": {
    "admin": 3000,
    "buyer_last_saldo": 68788,
    "buyer_sku_code": "BPLN",
    "customer_name": "LALAN",
    "customer_no": "532410749798",
    "desc": {
      "daya": 450,
      "detail": [
        {
          "admin": "3000",
          "denda": "0",
          "meter_akhir": "23175",
          "meter_awal": "23155",
          "nilai_tagihan": "9163",
          "periode": "NOP 25"
        }
      ],
      "lembar_tagihan": 1,
      "tarif": "R1"
    },
    "message": "Transaksi Sukses",
    "periode": "NOP 25",
    "price": 9923,
    "rc": "00",
    "ref_id": "53",
    "selling_price": 12163,
    "status": "Sukses"
  },
  "message": "Trx ID #53 Cek Tagihan berhasil SN: customer_no=532410749798, customer_name=LALAN, tarif=R1, daya=450, lembar_tagihan=1, periode=NOP 25, nilai_tagihan=9163, admin=3000, denda=0, meter_awal=23155, meter_akhir=23175"
}
```

## Payment Callback (Pascabayar - PLN Sukses)

**Callback URL (GET):**
```
{OTOMAX_CALLBACK_URL}?ref_id=54&status=Sukses&message=Trx ID #54 Pembayaran berhasil SN: customer_no=532410749798, customer_name=LALAN, tarif=R1, daya=450, lembar_tagihan=1, periode=NOV25, nilai_tagihan=9163, admin=3000, denda=0, meter_awal=00023155, meter_akhir=00023175, reff=2TKT21R391D7EE528946978D032F70FD
```

**Query Parameters:**
- `ref_id`: 54
- `status`: Sukses
- `message`: Trx ID #54 Pembayaran berhasil SN: customer_no=532410749798, customer_name=LALAN, tarif=R1, daya=450, lembar_tagihan=1, periode=NOV25, nilai_tagihan=9163, admin=3000, denda=0, meter_awal=00023155, meter_akhir=00023175, reff=2TKT21R391D7EE528946978D032F70FD

**Catatan:**
- Field `reff` di message berasal dari `data.sn` pada response Digiflazz payment sukses.
- Aplikasi melakukan callback async setelah payment selesai ke `OTOMAX_CALLBACK_URL`.

