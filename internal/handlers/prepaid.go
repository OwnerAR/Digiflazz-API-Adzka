package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"digiflazz-api/internal/config"
	"digiflazz-api/internal/digiflazz"
	"digiflazz-api/internal/logging"
	mssqlstore "digiflazz-api/internal/storage/mssql"
	sqlitestore "digiflazz-api/internal/storage/sqlite"
)

// PrepaidHandler handles OtomaX prepaid requests (topup)
type PrepaidHandler struct {
	cfg    *config.Config
	logger *logging.Logger
	sqlite *sqlitestore.Store
	mssql  *mssqlstore.Client
	dgf    *digiflazz.Client
}

func NewPrepaidHandler(cfg *config.Config, logger *logging.Logger, sqlite *sqlitestore.Store, mssql *mssqlstore.Client, dgf *digiflazz.Client) *PrepaidHandler {
	return &PrepaidHandler{cfg: cfg, logger: logger, sqlite: sqlite, mssql: mssql, dgf: dgf}
}

func (h *PrepaidHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	productCode := q.Get("product_code")
	if productCode == "" {
		productCode = q.Get("kode_produk")
	}
	customerNo := q.Get("customer_no")
	if customerNo == "" {
		customerNo = q.Get("nomor_pelanggan")
	}
	refID := q.Get("ref_id")
	if refID == "" {
		http.Error(w, "missing ref_id", http.StatusBadRequest)
		return
	}
	counter := q.Get("counter") // Read counter parameter
	// max_price is optional. If provided by OtomaX, it's guaranteed to be an integer.
	// If not provided or parsing fails, default to 0 (will fetch from DB later if needed).
	maxPriceStr := q.Get("max_price")
	maxPriceInt := int64(0)
	if maxPriceStr != "" {
		if parsed, err := strconv.ParseInt(maxPriceStr, 10, 64); err == nil {
			maxPriceInt = parsed
		} else {
			// Should not happen as OtomaX sends valid int, but handle for safety
			h.logger.Infof("failed to parse max_price ref_id=%s value=%s err=%v, using 0", refID, maxPriceStr, err)
		}
	}

	// Fill from OtomaX transaksi if missing (do this BEFORE cache logic)
	if productCode == "" || customerNo == "" {
		if tx, err := h.mssql.GetTransactionByRefID(r.Context(), refID); err == nil && tx != nil {
			if productCode == "" {
				productCode = tx.ProductCode
			}
			if customerNo == "" {
				customerNo = tx.CustomerNo
			}
		}
	}
	if productCode == "" || customerNo == "" {
		http.Error(w, "missing product/customer; not found in DB for provided ref_id", http.StatusBadRequest)
		return
	}

	// Optional cuan guard (PLN prepaid eligibility check)
	if q.Get("cuan") == "1" {
        refID = "PLN-Cuan-" + refID
		if err := h.ensurePLNCuanEligibility(r.Context(), productCode, customerNo, refID); err != nil {
			h.logger.Infof("pln cuan eligibility failed ref_id=%s product=%s customer=%s err=%v", refID, productCode, customerNo, err)
			w.Header().Set("Content-Type", "application/json")
			h.respond(w, http.StatusBadRequest, map[string]any{"message": err.Error()})
			return
		}
	}

	// check if cache is true, use sqlite cache
	// if from sqlite nil. fetch digiflazz with retry (max 3 times, 3s delay) and save to sqlite by customer_no and product_code
	cache := q.Get("cache")
	if cache == "true" {
		if h.sqlite != nil {
			if cached, err := h.sqlite.GetByCustomerNoAndProductCode(r.Context(), customerNo, productCode); err == nil && cached != nil {
				w.Header().Set("Content-Type", "application/json")
				var msg string
				if cached.SN != "" {
					msg = "Trx ID #" + refID + " Topup berhasil(Cache)  price: 0, SN: customer_no=" + customerNo + ", reff=" + cached.SN
				} else {
					msg = "Trx ID #" + refID + " Topup berhasil SN: customer_no=" + customerNo
				}
				h.respond(w, http.StatusOK, map[string]any{"message": msg})
				return
			}
		}
		// Cache miss: fetch from Digiflazz with retry (max 3 times, 3s delay) until success
		var res *digiflazz.TopupResult
		var err error
		maxRetries := 10
		retryDelay := 1 * time.Second

		// Build refID for Digiflazz (with counter prefix if provided)
		digiflazzRefID := buildRefIDForDigiflazz(refID, counter)

		for attempt := 1; attempt <= maxRetries; attempt++ {
			res, err = h.dgf.Topup(r.Context(), productCode, customerNo, digiflazzRefID, 0)
			if err != nil {
				h.logger.Errorf("digiflazz topup error (attempt %d/%d) ref_id=%s code=%s customer_no=%s err=%v", attempt, maxRetries, refID, productCode, customerNo, err)
				if attempt < maxRetries {
					time.Sleep(retryDelay)
					continue
				}
				// After max retries with error, return error without saving to cache
				http.Error(w, "topup failed after retries", http.StatusBadGateway)
				return
			}

			// Check if status is success
			statusLower := strings.ToLower(res.Status)
			isSuccess := statusLower == "sukses" || statusLower == "success" || statusLower == "berhasil" || statusLower == "ok"

			if isSuccess {
				// Success: save to cache and return
				if h.sqlite != nil {
					if err := h.sqlite.UpsertPrepaid(r.Context(), customerNo, productCode, res.Raw, res.SN); err != nil {
						h.logger.Errorf("failed to save prepaid cache: %v", err)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				// Ensure buyer_sku_code and customer_no are in response data
				var responseData map[string]any
				if res.Raw != nil {
					if data, ok := res.Raw["data"].(map[string]any); ok {
						responseData = data
						// Add missing fields if not present
						if _, ok := responseData["buyer_sku_code"]; !ok {
							responseData["buyer_sku_code"] = productCode
						}
						if _, ok := responseData["customer_no"]; !ok {
							responseData["customer_no"] = customerNo
						}
						if _, ok := responseData["ref_id"]; !ok {
							responseData["ref_id"] = refID
						}
					} else {
						responseData = make(map[string]any)
						responseData["buyer_sku_code"] = productCode
						responseData["customer_no"] = customerNo
						responseData["ref_id"] = refID
					}
				} else {
					responseData = map[string]any{
						"buyer_sku_code": productCode,
						"customer_no":    customerNo,
						"ref_id":         refID,
					}
				}
				var msg string
				if res.SN != "" {
					msg = "Topup berhasil SN: customer_no=" + customerNo + ", reff=" + res.SN
				} else {
					msg = "Topup berhasil SN: customer_no=" + customerNo
				}
				h.respond(w, http.StatusOK, map[string]any{"data": responseData, "message": msg})
				return
			}

			// Not success yet, retry if not last attempt
			h.logger.Infof("digiflazz topup not success yet (attempt %d/%d) ref_id=%s status=%s, retrying...", attempt, maxRetries, refID, res.Status)
			if attempt < maxRetries {
				time.Sleep(retryDelay)
			}
		}

		// After max retries without success: return response but DO NOT save to cache
		// Since callback goes directly to OtomaX, we can't update cache later
		// User should retry with cache=true again later or use normal flow (cache=false)
		w.Header().Set("Content-Type", "application/json")
		var msg string
		var responseData map[string]any
		if res != nil {
			// Use message from Digiflazz response
			if res.Message != "" {
				msg = res.Message
			} else {
				// Build message based on status
				statusLower := strings.ToLower(res.Status)
				if statusLower == "gagal" || statusLower == "failed" {
					msg = "Topup gagal"
				} else {
					msg = "Topup sedang diproses"
				}
			}
			if res.Raw != nil {
				if data, ok := res.Raw["data"].(map[string]any); ok {
					responseData = data
					// Ensure buyer_sku_code and customer_no are present
					if _, ok := responseData["buyer_sku_code"]; !ok || responseData["buyer_sku_code"] == "" {
						responseData["buyer_sku_code"] = productCode
					}
					if _, ok := responseData["customer_no"]; !ok || responseData["customer_no"] == "" {
						responseData["customer_no"] = customerNo
					}
					if _, ok := responseData["ref_id"]; !ok || responseData["ref_id"] == "" {
						responseData["ref_id"] = refID
					}
				} else {
					// Create response data if not present
					responseData = map[string]any{
						"buyer_sku_code": productCode,
						"customer_no":    customerNo,
						"ref_id":         refID,
						"status":         res.Status,
						"message":        msg,
					}
				}
			} else {
				responseData = map[string]any{
					"buyer_sku_code": productCode,
					"customer_no":    customerNo,
					"ref_id":         refID,
					"status":         res.Status,
					"message":        msg,
				}
			}
		} else {
			msg = "Topup sedang diproses"
			responseData = map[string]any{
				"buyer_sku_code": productCode,
				"customer_no":    customerNo,
				"status":         "Process",
				"message":        msg,
				"ref_id":         refID,
			}
		}
		h.logger.Infof("digiflazz topup not success after %d retries ref_id=%s, returning without cache", maxRetries, refID)
		h.respond(w, http.StatusOK, map[string]any{"data": responseData, "message": msg})
		return
	}

	// Normal flow (cache=false or not set): proceed with topup
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(h.cfg.RequestTimeoutMs)*time.Millisecond)
	defer cancel()

	// if max_price is 0, fetch max_price from transaksi.harga
	if maxPriceInt == 0 {
		if harga, err := h.mssql.GetTransactionHargaByRef(ctx, refID); err == nil {
			maxPriceInt = harga
		}
	}

	// Build refID for Digiflazz (with counter prefix if provided)
	digiflazzRefID := buildRefIDForDigiflazz(refID, counter)
	res, err := h.dgf.Topup(ctx, productCode, customerNo, digiflazzRefID, maxPriceInt)
	if err != nil {
		http.Error(w, "topup failed", http.StatusBadGateway)
		return
	}

	// Build friendly message (include reff if SN present)
	var msg string
	if strings.EqualFold(res.Status, "sukses") || strings.EqualFold(res.Status, "success") || strings.EqualFold(res.Status, "berhasil") || strings.EqualFold(res.Status, "ok") {
		if res.SN != "" {
			msg = "Trx ID #" + refID + " Topup berhasil SN: customer_no=" + customerNo + ", reff=" + res.SN
		} else {
			msg = "Trx ID #" + refID + " Topup berhasil SN: customer_no=" + customerNo
		}
	} else {
		msg = res.Message
	}

	w.Header().Set("Content-Type", "application/json")
	h.respond(w, http.StatusOK, map[string]any{"data": res.Raw["data"], "message": msg})
}

// reuse JSON responder from otomax.go via small wrapper to avoid export
// buildRefIDForDigiflazz modifies refID based on counter parameter
// If counter is provided, returns C{counter}-{refID}, otherwise returns refID as-is
func buildRefIDForDigiflazz(refID, counter string) string {
	if counter != "" && counter != "0" {
		return fmt.Sprintf("C%s-%s", counter, refID)
	}
	return refID
}

func (h *PrepaidHandler) respond(w http.ResponseWriter, code int, body any) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func (h *PrepaidHandler) ensurePLNCuanEligibility(ctx context.Context, productCode, customerNo, refID string) error {
	validateCtx, cancel := context.WithTimeout(ctx, time.Duration(h.cfg.RequestTimeoutMs)*time.Millisecond)
	defer cancel()

	res, err := h.dgf.InquiryPLN(validateCtx, customerNo)
	if err != nil {
		return fmt.Errorf("failed to inquiry PLN data: %w, RefID: %s", err, refID)
	}

	if !strings.EqualFold(res.Status, "sukses") {
		if res.Message != "" {
			return fmt.Errorf("PLN inquiry rejected: %s, RefID: %s", res.Message, refID)
		}
		return fmt.Errorf("PLN inquiry rejected with status %s, RefID: %s", res.Status, refID)
	}

	golongan, daya, err := parseSegmentPower(res.SegmentPower)
	if err != nil {
		return fmt.Errorf("invalid PLN inquiry response: %w, RefID: %s", err, refID)
	}

	if daya != 450 {
		return fmt.Errorf("PLN daya must be 450, got %d, RefID: %s", daya, refID)
	}
	if !strings.EqualFold(golongan, "R1") {
		return fmt.Errorf("PLN golongan must be R1, got %s, RefID: %s", golongan, refID)
	}
	return nil
}

func parseSegmentPower(segment string) (string, int64, error) {
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return "", 0, fmt.Errorf("segment_power missing")
	}

	parts := strings.Split(segment, "/")
	golongan := strings.TrimSpace(parts[0])
	if golongan == "" {
		return "", 0, fmt.Errorf("segment_power missing golongan")
	}

	if len(parts) < 2 {
		return "", 0, fmt.Errorf("segment_power missing daya")
	}

	dayaStr := strings.TrimSpace(parts[1])
	if dayaStr == "" {
		return "", 0, fmt.Errorf("segment_power missing daya value")
	}
	dayaStr = strings.ReplaceAll(dayaStr, ".", "")
	daya, err := strconv.ParseInt(dayaStr, 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("invalid daya value: %w", err)
	}
	return golongan, daya, nil
}
