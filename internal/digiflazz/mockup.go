package digiflazz

import (
	"context"
)

// MockupData stores mockup responses for testing
type MockupData struct {
	InquirySukses map[string]any
	PaymentSukses map[string]any
}

var mockupData *MockupData

func initMockupData() {
	if mockupData != nil {
		return
	}
	mockupData = &MockupData{
		InquirySukses: map[string]any{
			"data": map[string]any{
				"admin":            3000,
				"buyer_last_saldo": 68788,
				"buyer_sku_code":   "BPLN",
				"customer_name":    "LALAN",
				"customer_no":      "532410749798",
				"desc": map[string]any{
					"daya": 450,
					"detail": []any{
						map[string]any{
							"admin":         "3000",
							"denda":         "0",
							"meter_akhir":   "23175",
							"meter_awal":    "23155",
							"nilai_tagihan": "9163",
							"periode":       "NOP 25",
						},
					},
					"lembar_tagihan": 1,
					"tarif":          "R1",
				},
				"message":       "Transaksi Sukses",
				"periode":       "NOP 25",
				"price":         9923,
				"rc":            "00",
				"ref_id":        "53",
				"selling_price": 12163,
				"status":        "Sukses",
			},
			"message": "Trx ID #53 Cek Tagihan berhasil SN: customer_no=532410749798, customer_name=LALAN, tarif=R1, daya=450, lembar_tagihan=1, periode=NOP 25, nilai_tagihan=9163, admin=3000, denda=0, meter_awal=23155, meter_akhir=23175",
		},
		PaymentSukses: map[string]any{
			"data": map[string]any{
				"admin":            3000,
				"buyer_last_saldo": 68788,
				"buyer_sku_code":   "BPLN",
				"customer_name":    "LALAN",
				"customer_no":      "532410749798",
				"desc": map[string]any{
					"daya": 450,
					"detail": []any{
						map[string]any{
							"admin":         "3000",
							"denda":         "0",
							"meter_akhir":   "00023175",
							"meter_awal":    "00023155",
							"nilai_tagihan": "9163",
							"periode":       "NOV25",
						},
					},
					"lembar_tagihan": 1,
					"tarif":          "R1",
				},
				"message": "Transaksi Sukses",
				"periode": "NOV25",
				"price":   9923,
				"rc":      "00",
				"ref_id":  "54",
				"sn":      "2TKT21R391D7EE528946978D032F70FD",
				"status":  "Sukses",
			},
			"message": "Trx ID #54 Pembayaran berhasil SN: customer_no=532410749798, customer_name=LALAN, tarif=R1, daya=450, lembar_tagihan=1, periode=NOV25, nilai_tagihan=9163, admin=3000, denda=0, meter_awal=00023155, meter_akhir=00023175, reff=2TKT21R391D7EE528946978D032F70FD",
		},
	}
}

// getMockupInquiry returns mockup inquiry response
func getMockupInquiry(ctx context.Context, productCode, customerNo, refID string) (*InquiryResult, error) {
	initMockupData()

	// Clone and update ref_id in mockup data
	raw := make(map[string]any)
	if dataMap, ok := mockupData.InquirySukses["data"].(map[string]any); ok {
		dataCopy := make(map[string]any)
		for k, v := range dataMap {
			dataCopy[k] = v
		}
		dataCopy["ref_id"] = refID
		dataCopy["buyer_sku_code"] = productCode
		dataCopy["customer_no"] = customerNo
		raw["data"] = dataCopy
	}

	res := &InquiryResult{Raw: raw}
	if data, ok := raw["data"].(map[string]any); ok {
		if v, ok := data["status"].(string); ok {
			res.Status = v
		}
		if v, ok := data["message"].(string); ok {
			res.Message = v
		}
		if b, ok := asInt64(data["price"]); ok {
			res.BillAmount = b
		}
	}

	return res, nil
}

// getMockupPayment returns mockup payment response
func getMockupPayment(ctx context.Context, productCode, customerNo, refID string) (*PaymentResult, error) {
	initMockupData()

	// Clone and update ref_id in mockup data
	raw := make(map[string]any)
	if dataMap, ok := mockupData.PaymentSukses["data"].(map[string]any); ok {
		dataCopy := make(map[string]any)
		for k, v := range dataMap {
			dataCopy[k] = v
		}
		dataCopy["ref_id"] = refID
		dataCopy["buyer_sku_code"] = productCode
		dataCopy["customer_no"] = customerNo
		raw["data"] = dataCopy
	}

	res := &PaymentResult{Raw: raw}
	if data, ok := raw["data"].(map[string]any); ok {
		if v, ok := data["status"].(string); ok {
			res.Status = v
		}
		if v, ok := data["message"].(string); ok {
			res.Message = v
		}
		if b, ok := asInt64(data["price"]); ok {
			res.PaidAmount = b
		}
	}

	return res, nil
}

// getMockupTopup returns mockup topup response
func getMockupTopup(ctx context.Context, productCode, customerNo, refID string, maxPrice int64) (*TopupResult, error) {
	initMockupData()

	// Create mockup topup response
	raw := map[string]any{
		"data": map[string]any{
			"status":         "Sukses",
			"message":        "Transaksi Sukses",
			"sn":             "MOCKUP" + refID,
			"buyer_sku_code": productCode,
			"customer_no":    customerNo,
			"ref_id":         refID,
			"price":          maxPrice,
		},
	}

	res := &TopupResult{Raw: raw}
	if data, ok := raw["data"].(map[string]any); ok {
		if v, ok := data["status"].(string); ok {
			res.Status = v
		}
		if v, ok := data["message"].(string); ok {
			res.Message = v
		}
		if v, ok := data["sn"].(string); ok {
			res.SN = v
		}
		if p, ok := asInt64(data["price"]); ok {
			res.Price = p
		}
	}

	return res, nil
}

func getMockupPLNInquiry(ctx context.Context, customerNo string) (*PLNInquiryResult, error) {
	raw := map[string]any{
		"data": map[string]any{
			"status":        "Sukses",
			"message":       "Transaksi Sukses",
			"rc":            "00",
			"customer_no":   customerNo,
			"meter_no":      customerNo,
			"subscriber_id": "MOCK-" + customerNo,
			"name":          "PLN CUSTOMER",
			"segment_power": "R1 /000000450",
		},
	}
	return &PLNInquiryResult{
		Status:       "Sukses",
		Message:      "Transaksi Sukses",
		RC:           "00",
		CustomerNo:   customerNo,
		MeterNo:      customerNo,
		SubscriberID: "MOCK-" + customerNo,
		Name:         "PLN CUSTOMER",
		SegmentPower: "R1 /000000450",
		Raw:          raw,
	}, nil
}
