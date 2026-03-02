package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"digiflazz-api/internal/config"
	"digiflazz-api/internal/digiflazz"
	"digiflazz-api/internal/domain"
	"digiflazz-api/internal/logging"
	"digiflazz-api/internal/otomax"
	mssqlstore "digiflazz-api/internal/storage/mssql"
	sqlitestore "digiflazz-api/internal/storage/sqlite"
)

type OtomaxHandler struct {
	cfg    *config.Config
	logger *logging.Logger
	sqlite *sqlitestore.Store
	mssql  *mssqlstore.Client
	dgf    *digiflazz.Client
	otomax *otomax.Client
}

func NewOtomaxHandler(cfg *config.Config, logger *logging.Logger, sqlite *sqlitestore.Store, mssql *mssqlstore.Client, dgf *digiflazz.Client, otomax *otomax.Client) *OtomaxHandler {
	return &OtomaxHandler{cfg: cfg, logger: logger, sqlite: sqlite, mssql: mssql, dgf: dgf, otomax: otomax}
}

// encodeMessageForQuery encodes message for URL query string (callback)
// Based on testing with curl, server OtomaX works when all special characters are encoded
// We encode #, &, %, and space to ensure compatibility
// = inside the message value is safe because it's already within the value part
func encodeMessageForQuery(message string) string {
	// Based on curl testing, server works when characters are properly encoded
	// We encode: # as %23, & as %26, % as %25, space as %20
	// = is safe inside the value (message=...&) so we don't encode it
	var result strings.Builder
	for _, r := range message {
		switch r {
		case '#':
			result.WriteString("%23")
		case '&':
			result.WriteString("%26")
		case '%':
			result.WriteString("%25")
		case ' ':
			result.WriteString("%20")
		default:
			// Keep = and other characters as-is
			result.WriteRune(r)
		}
	}
	return result.String()
}

// encodeMessageForResponse encodes message for response body (double request)
// Response body is not a URL, so we only need to encode characters that break the format
// # can remain as-is in response body
func encodeMessageForResponse(message string) string {
	// For response body (text/plain), we only encode characters that would break the format
	// # can remain as-is since it's not a URL
	return message
}

func (h *OtomaxHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Log IP/method/path (local app)
	clientIP := clientIPFromRequest(r)
	h.logger.Infof("incoming request ip=%s method=%s path=%s", clientIP, r.Method, r.URL.Path)

	q := r.URL.Query()
	action := strings.ToLower(q.Get("action"))
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

	// Default action to inquiry if not provided
	if action == "" {
		action = string(domain.ActionInquiry)
	}

	// If product/customer not provided, fetch from OtomaX API by ref_id
	if productCode == "" || customerNo == "" {
		h.logger.Infof("querying OtomaX API for ref_id=%s to fill missing product/customer", refID)
		tx, err := h.otomax.GetTrx(r.Context(), refID)
		if err != nil {
			h.logger.Errorf("otomax GetTrx error for ref_id=%s: %v", refID, err)
		}
		if tx == nil {
			h.logger.Errorf("otomax: no transaction found for ref_id=%s", refID)
		} else if !tx.OK || tx.Result.KodeProduk == "" {
			h.logger.Errorf("otomax: transaction response invalid or empty for ref_id=%s", refID)
		} else {
			h.logger.Infof("otomax: found transaction ref_id=%s kode=%v product_code=%s customer_no=%s kode_reseller=%s",
				refID, tx.Result.Kode, tx.Result.KodeProduk, tx.Result.Tujuan, tx.Result.KodeReseller)
			if productCode == "" {
				productCode = tx.Result.KodeProduk
			}
			if customerNo == "" {
				customerNo = tx.Result.Tujuan
			}
		}
	}

	if productCode == "" || customerNo == "" {
		http.Error(w, "missing product/customer; not found in DB for provided ref_id", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(h.cfg.RequestTimeoutMs)*time.Millisecond)
	defer cancel()

	switch action {
	case string(domain.ActionInquiry):
		h.handleInquiry(ctx, w, productCode, customerNo, refID)
		return
	case string(domain.ActionPayment):
		h.handlePayment(ctx, w, productCode, customerNo, refID)
		return
	default:
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}
}

func (h *OtomaxHandler) handleInquiry(ctx context.Context, w http.ResponseWriter, productCode, customerNo, refID string) {
	// Always perform inquiry to return original Digiflazz response
	res, err := h.dgf.Inquiry(ctx, productCode, customerNo, refID)
	if err != nil {
		h.logger.Errorf("digiflazz inquiry error ref_id=%s code=%s customer_no=%s err=%v", refID, productCode, customerNo, err)
		http.Error(w, "inquiry failed", http.StatusBadGateway)
		return
	}

	bill := res.BillAmount
	admin := h.cfg.DefaultAdminFee
	margin := h.cfg.DefaultMargin
	selling := bill + admin + margin

	t := &domain.Transaction{
		RefID:           refID,
		Action:          domain.ActionInquiry,
		ProductCode:     productCode,
		CustomerNo:      customerNo,
		BillAmount:      bill,
		AdminFee:        admin,
		Margin:          margin,
		SellingPrice:    selling,
		ExternalStatus:  string(mapStatus(res.Status)),
		ExternalMessage: res.Message,
	}
	_ = h.sqlite.UpsertInquiry(ctx, t)

	// On inquiry success: update SN in transaksi (status update handled by OtomaX)
	var messageStr string
	responseData := res.Raw["data"]
	if s := strings.ToLower(res.Status); s == "sukses" || s == "success" || s == "berhasil" || s == "ok" {
		if rawData, ok := res.Raw["data"].(map[string]any); ok {
			if rc, ok := rawData["rc"].(string); ok && rc != "" {
				_ = rc // rc kept for audit if needed
			}

			// Add nilai_tagihan if not present (fallback to selling_price)
			var nilaiTagihanStr string
			if _, hasNilaiTagihan := rawData["nilai_tagihan"]; !hasNilaiTagihan {
				// Check if nilai_tagihan exists in desc.detail
				var nilaiTagihanFound bool
				if desc, ok := rawData["desc"].(map[string]any); ok {
					// Get lembar_tagihan to determine if we need to combine multiple values
					var lembarTagihanInt int
					if lt, ok := desc["lembar_tagihan"].(float64); ok {
						lembarTagihanInt = int(lt)
					} else if lt, ok := desc["lembar_tagihan"].(int64); ok {
						lembarTagihanInt = int(lt)
					} else if lt, ok := desc["lembar_tagihan"].(int); ok {
						lembarTagihanInt = lt
					} else if lt, ok := desc["lembar_tagihan"].(string); ok {
						if ltParsed, err := strconv.Atoi(lt); err == nil {
							lembarTagihanInt = ltParsed
						}
					}

					if detail, ok := desc["detail"].([]any); ok && len(detail) > 0 {
						// If lembar_tagihan > 1, combine all nilai_tagihan with # separator
						if lembarTagihanInt > 1 {
							nilaiTagihanParts := make([]string, 0, len(detail))
							for _, detailItem := range detail {
								if detailMap, ok := detailItem.(map[string]any); ok {
									if nt, ok := detailMap["nilai_tagihan"]; ok && nt != nil {
										var ntStr string
										if ntStrVal, ok := nt.(string); ok && ntStrVal != "" {
											ntStr = ntStrVal
										} else if ntFloat, ok := nt.(float64); ok && ntFloat > 0 {
											ntStr = fmt.Sprintf("%.0f", ntFloat)
										} else if ntInt, ok := nt.(int64); ok && ntInt > 0 {
											ntStr = fmt.Sprintf("%d", ntInt)
										} else if ntInt, ok := nt.(int); ok && ntInt > 0 {
											ntStr = fmt.Sprintf("%d", ntInt)
										}
										if ntStr != "" {
											nilaiTagihanParts = append(nilaiTagihanParts, ntStr)
										}
									}
								}
							}
							if len(nilaiTagihanParts) > 0 {
								nilaiTagihanStr = strings.Join(nilaiTagihanParts, "#")
								rawData["nilai_tagihan"] = nilaiTagihanStr
								nilaiTagihanFound = true
							}
						} else {
							// Single lembar_tagihan: extract from detail[0] only
							if firstDetail, ok := detail[0].(map[string]any); ok {
								if nt, ok := firstDetail["nilai_tagihan"]; ok && nt != nil {
									// nilai_tagihan exists in detail, use it
									if ntStr, ok := nt.(string); ok && ntStr != "" {
										rawData["nilai_tagihan"] = ntStr
										nilaiTagihanStr = ntStr
										nilaiTagihanFound = true
									} else if ntFloat, ok := nt.(float64); ok && ntFloat > 0 {
										nilaiTagihanStr = fmt.Sprintf("%.0f", ntFloat)
										rawData["nilai_tagihan"] = nilaiTagihanStr
										nilaiTagihanFound = true
									} else if ntInt, ok := nt.(int64); ok && ntInt > 0 {
										nilaiTagihanStr = fmt.Sprintf("%d", ntInt)
										rawData["nilai_tagihan"] = nilaiTagihanStr
										nilaiTagihanFound = true
									} else if ntInt, ok := nt.(int); ok && ntInt > 0 {
										nilaiTagihanStr = fmt.Sprintf("%d", ntInt)
										rawData["nilai_tagihan"] = nilaiTagihanStr
										nilaiTagihanFound = true
									}
								}
							}
						}
					}
				}

				// If nilai_tagihan still not found, use selling_price as fallback
				if !nilaiTagihanFound {
					if sp, ok := rawData["selling_price"].(float64); ok && sp > 0 {
						nilaiTagihanStr = fmt.Sprintf("%.0f", sp)
						rawData["nilai_tagihan"] = nilaiTagihanStr
					} else if sp, ok := rawData["selling_price"].(int64); ok && sp > 0 {
						nilaiTagihanStr = fmt.Sprintf("%d", sp)
						rawData["nilai_tagihan"] = nilaiTagihanStr
					} else if sp, ok := rawData["selling_price"].(int); ok && sp > 0 {
						nilaiTagihanStr = fmt.Sprintf("%d", sp)
						rawData["nilai_tagihan"] = nilaiTagihanStr
					} else if sp, ok := rawData["selling_price"].(string); ok && sp != "" {
						nilaiTagihanStr = sp
						rawData["nilai_tagihan"] = nilaiTagihanStr
					}
				}
			} else {
				// nilai_tagihan already exists, get its value for message
				if nt, ok := rawData["nilai_tagihan"].(string); ok {
					nilaiTagihanStr = nt
				} else if nt, ok := rawData["nilai_tagihan"].(float64); ok {
					nilaiTagihanStr = fmt.Sprintf("%.0f", nt)
				} else if nt, ok := rawData["nilai_tagihan"].(int64); ok {
					nilaiTagihanStr = fmt.Sprintf("%d", nt)
				} else if nt, ok := rawData["nilai_tagihan"].(int); ok {
					nilaiTagihanStr = fmt.Sprintf("%d", nt)
				}
			}

			// Remove "message" from data to avoid duplication (we'll add our own message at top level)
			delete(rawData, "message")

			// Build SN summary (plain string, no JSON) with nilai_tagihan included
			var cno, cname, dstr string
			if v, ok := rawData["customer_no"].(string); ok {
				cno = v
			}
			if v, ok := rawData["customer_name"].(string); ok {
				cname = v
			}

			// Get desc string first
			if dv, ok := rawData["desc"]; ok {
				dstr = stringifyDesc(dv)
			}

			// Check if stringifyDesc already contains nilai_tagihan
			hasNilaiTagihanInDesc := strings.Contains(dstr, "nilai_tagihan=")

			// Build message with nilai_tagihan included
			messageParts := []string{
				fmt.Sprintf("Trx ID #%s Cek Tagihan %s berhasil SN: customer_no=%s, customer_name=%s", refID, productCode, cno, cname),
			}
			// Only add nilai_tagihan explicitly if it's not already in stringifyDesc result
			if !hasNilaiTagihanInDesc && nilaiTagihanStr != "" {
				messageParts = append(messageParts, fmt.Sprintf("nilai_tagihan=%s", nilaiTagihanStr))
			}
			if dstr != "" {
				messageParts = append(messageParts, dstr)
			}
			messageStr = strings.Join(messageParts, ", ")
			responseData = rawData
		}
	}
	responseFinal := map[string]any{"data": responseData, "message": messageStr}
	// Return original Digiflazz response back to OtomaX (with added nilai_tagihan if needed)
	h.respond(w, http.StatusOK, responseFinal)
}

func (h *OtomaxHandler) handlePayment(ctx context.Context, w http.ResponseWriter, productCode, customerNo, refID string) {
	var inqRaw map[string]any
	var totalHarga int64
	var kodeReseller string

	// Check if payment was already attempted for this ref_id
	// If yes, always check status to Digiflazz API (regardless of time)
	existingTx, err := h.sqlite.GetByRefID(ctx, refID)
	if err == nil && existingTx != nil && existingTx.Action == domain.ActionPayment {
		// Always perform status check to Digiflazz API
		statusRes, statusErr := h.dgf.Status(ctx, productCode, customerNo, refID)
		if statusErr == nil && statusRes != nil {
			// Check if status is final (Sukses, Gagal, or other final status)
			statusLower := strings.ToLower(statusRes.Status)
			rc := statusRes.RC
			// Check if status is final (not Process/Pending)
			isFinal := rc == "00" || statusLower == "sukses" || statusLower == "success" || statusLower == "berhasil" || statusLower == "ok" || statusLower == "gagal" || statusLower == "failed"
			if isFinal {
				// Status is final, return it in same format as callback (query parameters)
				h.logger.Infof("payment status check ref_id=%s status=%s rc=%s message=%s", refID, statusRes.Status, rc, statusRes.Message)
				var messageStr string
				var nilaiTagihan string
				var lembarTagihan string
				var totalTagihan string

				// Use callback message from SQLite if available (already built and saved during payment)
				if existingTx.ExternalMessage != "" {
					// Callback message already saved, use it directly (same as what was sent in callback)
					// IMPORTANT: Use the exact message from SQLite without any modification
					messageStr = existingTx.ExternalMessage
					h.logger.Infof("using saved callback message from SQLite for ref_id=%s message=%s", refID, messageStr)
				} else {
					// Callback message not available, build from raw data
					var rawDataToUse map[string]any
					if existingTx.RawData != nil && len(existingTx.RawData) > 0 {
						rawDataToUse = existingTx.RawData
					} else if statusRes.Raw != nil {
						rawDataToUse = statusRes.Raw
					}

					if rawDataToUse != nil {
						if d, ok := rawDataToUse["data"].(map[string]any); ok {
							// Build message exactly same as callback format
							statusLower := strings.ToLower(statusRes.Status)
							if statusLower == "sukses" || statusLower == "success" || statusLower == "berhasil" || statusLower == "ok" {
								// Success: use exact same format as callback
								var cno, cname, dstr, reff string
								if v, ok := d["customer_no"].(string); ok {
									cno = v
								}
								if v, ok := d["customer_name"].(string); ok {
									cname = v
								}
								if dv, ok := d["desc"]; ok {
									dstr = stringifyDesc(dv)
								}
								if v, ok := d["sn"].(string); ok {
									reff = v
								}
								// Exact same format as callback message
								messageStr = fmt.Sprintf("Trx ID #%s Pembayaran %s berhasil SN: customer_no=%s, customer_name=%s%s%s", refID, productCode, cno, cname, prefixIfContent(", ", dstr), prefixIfContent(", reff=", reff))
							} else {
								// Failed: use message directly from Digiflazz (same as callback)
								messageStr = statusRes.Message
							}
						} else {
							// Use message directly from Digiflazz (same as callback)
							messageStr = statusRes.Message
						}
					} else {
						// Use message directly from Digiflazz (same as callback)
						messageStr = statusRes.Message
					}
				}

				// Extract nilai_tagihan, lembar_tagihan, and total_tagihan from raw data (same as callback)
				var rawDataToUse map[string]any
				if existingTx.RawData != nil && len(existingTx.RawData) > 0 {
					rawDataToUse = existingTx.RawData
				} else if statusRes.Raw != nil {
					rawDataToUse = statusRes.Raw
				}

				if rawDataToUse != nil {
					if d, ok := rawDataToUse["data"].(map[string]any); ok {
						// Extract total_tagihan from selling_price
						if sp, ok := d["selling_price"].(float64); ok {
							totalTagihan = fmt.Sprintf("%.0f", sp)
						} else if sp, ok := d["selling_price"].(int64); ok {
							totalTagihan = fmt.Sprintf("%d", sp)
						} else if sp, ok := d["selling_price"].(int); ok {
							totalTagihan = fmt.Sprintf("%d", sp)
						} else if sp, ok := d["selling_price"].(string); ok {
							totalTagihan = sp
						}

						// Extract nilai_tagihan and lembar_tagihan from desc
						if desc, ok := d["desc"].(map[string]any); ok {
							// Extract lembar_tagihan
							if lt, ok := desc["lembar_tagihan"].(float64); ok {
								lembarTagihan = fmt.Sprintf("%.0f", lt)
							} else if lt, ok := desc["lembar_tagihan"].(int64); ok {
								lembarTagihan = fmt.Sprintf("%d", lt)
							} else if lt, ok := desc["lembar_tagihan"].(int); ok {
								lembarTagihan = fmt.Sprintf("%d", lt)
							} else if lt, ok := desc["lembar_tagihan"].(string); ok {
								lembarTagihan = lt
							}

							// Extract nilai_tagihan from desc.detail
							if detail, ok := desc["detail"].([]any); ok && len(detail) > 0 {
								var lembarTagihanInt int
								if lembarTagihan != "" {
									if lt, err := strconv.Atoi(lembarTagihan); err == nil {
										lembarTagihanInt = lt
									} else if lt, err := strconv.ParseFloat(lembarTagihan, 64); err == nil {
										lembarTagihanInt = int(lt)
									}
								}

								// If lembar_tagihan > 1, combine all nilai_tagihan with # separator
								if lembarTagihanInt > 1 {
									nilaiTagihanParts := make([]string, 0, len(detail))
									for _, detailItem := range detail {
										if detailMap, ok := detailItem.(map[string]any); ok {
											var ntStr string
											if nt, ok := detailMap["nilai_tagihan"].(string); ok {
												ntStr = nt
											} else if nt, ok := detailMap["nilai_tagihan"].(float64); ok {
												ntStr = fmt.Sprintf("%.0f", nt)
											} else if nt, ok := detailMap["nilai_tagihan"].(int64); ok {
												ntStr = fmt.Sprintf("%d", nt)
											} else if nt, ok := detailMap["nilai_tagihan"].(int); ok {
												ntStr = fmt.Sprintf("%d", nt)
											}
											if ntStr != "" {
												nilaiTagihanParts = append(nilaiTagihanParts, ntStr)
											}
										}
									}
									if len(nilaiTagihanParts) > 0 {
										nilaiTagihan = strings.Join(nilaiTagihanParts, "#")
									}
								} else {
									// Single lembar_tagihan: extract from detail[0] only
									if firstDetail, ok := detail[0].(map[string]any); ok {
										if nt, ok := firstDetail["nilai_tagihan"].(string); ok {
											nilaiTagihan = nt
										} else if nt, ok := firstDetail["nilai_tagihan"].(float64); ok {
											nilaiTagihan = fmt.Sprintf("%.0f", nt)
										} else if nt, ok := firstDetail["nilai_tagihan"].(int64); ok {
											nilaiTagihan = fmt.Sprintf("%d", nt)
										}
									}
								}
							}
						}

						// Fallback: if nilai_tagihan is not found, use selling_price
						if nilaiTagihan == "" {
							nilaiTagihan = totalTagihan
						}
					}
				}

				// Build query string in same format as callback (same order and encoding)
				// Use url.Values but build manually to maintain exact order like callback
				// Note: For response body, we use encodeMessageForResponse (doesn't encode #)
				// For callback URL, we use encodeMessageForQuery (encodes # as %23)
				params := make([]string, 0, 6)
				params = append(params, "ref_id="+url.QueryEscape(refID))
				params = append(params, "status="+url.QueryEscape(statusRes.Status))
				params = append(params, "message="+encodeMessageForResponse(messageStr))
				if nilaiTagihan != "" {
					params = append(params, "nilai_tagihan="+url.QueryEscape(nilaiTagihan))
				}
				if lembarTagihan != "" {
					params = append(params, "lembar_tagihan="+url.QueryEscape(lembarTagihan))
				}
				if totalTagihan != "" {
					params = append(params, "total_tagihan="+url.QueryEscape(totalTagihan))
				}
				queryString := strings.Join(params, "&")

				// Log the exact message and query string being returned
				h.logger.Infof("returning double request response ref_id=%s status=%s message=%s query=%s", refID, statusRes.Status, messageStr, queryString)

				// Return response in query string format (same as callback)
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(queryString))
				return
			}
			// Status is still pending/not final
			// Check time to prevent double payment
			timeSinceUpdate := time.Since(existingTx.UpdatedAt)
			if timeSinceUpdate < 1*time.Minute {
				// Less than 1 minute has passed, return Process status to prevent double payment
				h.logger.Infof("payment status check ref_id=%s status=%s (not final, <1min), returning Process to prevent double payment", refID, statusRes.Status)
				processData := map[string]any{
					"ref_id":         refID,
					"buyer_sku_code": productCode,
					"customer_no":    customerNo,
					"status":         "Process",
					"message":        "Transaksi sedang diproses",
					"rc":             "03",
				}
				messageStr := fmt.Sprintf("Trx ID #%s Transaksi sedang diproses", refID)
				h.respond(w, http.StatusOK, map[string]any{"data": processData, "message": messageStr})
				return
			}
			// More than 1 minute has passed, status still pending, continue with normal payment flow
			h.logger.Infof("payment status check ref_id=%s status=%s (not final, >1min), continuing with payment", refID, statusRes.Status)
		} else if statusErr != nil {
			h.logger.Errorf("payment status check error ref_id=%s err=%v", refID, statusErr)
			// Continue with normal payment flow if status check fails
		}
	}

	// Re-inquiry to validate current bill; on success, compute prices and check reseller balance
	inqRes, err := h.dgf.Inquiry(ctx, productCode, customerNo, refID)
	if err != nil {
		h.logger.Errorf("digiflazz re-inquiry before payment failed ref_id=%s code=%s customer_no=%s err=%v", refID, productCode, customerNo, err)
		// Return inquiry error response to OtomaX
		errorData := map[string]any{
			"ref_id":         refID,
			"buyer_sku_code": productCode,
			"customer_no":    customerNo,
			"status":         "Gagal",
			"message":        "Cek Tagihan gagal: " + err.Error(),
			"rc":             "40",
		}
		var messageStr string
		messageStr = fmt.Sprintf("Trx ID #%s Cek Tagihan gagal: %s", refID, err.Error())
		h.respond(w, http.StatusOK, map[string]any{"data": errorData, "message": messageStr})
		return
	}

	// Check if inquiry was successful (rc="00" or status="Sukses")
	if inqRes.Raw != nil {
		if d, ok := inqRes.Raw["data"].(map[string]any); ok {
			inqRaw = d
			// Check rc and status from inquiry response
			rc, _ := d["rc"].(string)
			status, _ := d["status"].(string)
			statusLower := strings.ToLower(status)
			if rc != "00" || (statusLower != "sukses" && statusLower != "success" && statusLower != "berhasil" && statusLower != "ok") {
				// Inquiry failed or not successful, return error response
				h.logger.Errorf("digiflazz re-inquiry not successful ref_id=%s rc=%s status=%s", refID, rc, status)
				var messageStr string
				if msg, ok := d["message"].(string); ok && msg != "" {
					messageStr = fmt.Sprintf("Trx ID #%s %s", refID, msg)
				} else {
					messageStr = fmt.Sprintf("Trx ID #%s Cek Tagihan gagal: rc=%s, status=%s", refID, rc, status)
				}
				h.respond(w, http.StatusOK, map[string]any{"data": inqRaw, "message": messageStr})
				return
			}
		}
	}

	// Get transaction from OtomaX API to get harga_jual and kode_reseller
	tx, err := h.otomax.GetTrx(ctx, refID)
	if err != nil {
		h.logger.Errorf("otomax GetTrx error for ref_id=%s: %v", refID, err)
		errorData := map[string]any{
			"ref_id":         refID,
			"buyer_sku_code": productCode,
			"customer_no":    customerNo,
			"status":         "Gagal",
			"message":        "Gagal mendapatkan data transaksi dari OtomaX",
			"rc":             "40",
		}
		h.respond(w, http.StatusOK, map[string]any{"data": errorData, "message": fmt.Sprintf("Trx ID #%s Gagal mendapatkan data transaksi", refID)})
		return
	}

	if tx == nil || !tx.OK {
		if tx != nil && !tx.OK {
			h.logger.Errorf("otomax GetTrx returned ok=false for ref_id=%s", refID)
		} else {
			h.logger.Errorf("otomax GetTrx returned nil for ref_id=%s", refID)
		}
		errorData := map[string]any{
			"ref_id":         refID,
			"buyer_sku_code": productCode,
			"customer_no":    customerNo,
			"status":         "Gagal",
			"message":        "Transaksi tidak ditemukan di OtomaX",
			"rc":             "40",
		}
		h.respond(w, http.StatusOK, map[string]any{"data": errorData, "message": fmt.Sprintf("Trx ID #%s Transaksi tidak ditemukan", refID)})
		return
	}

	hargaBeli := inqRes.BillAmount
	// totalHarga = harga_beli + harga_jual. Ambil harga_jual: utamakan dari produk, fallback transaksi.harga = harga_jual (langsung, jangan dikurangi hargaBeli supaya tidak minus)
	hargaJual := int64(0)
	if h.mssql != nil {
		if hj, err := h.mssql.GetProductHargaJualByCode(ctx, productCode); err == nil && hj > 0 {
			hargaJual = hj
		}
	}
	if hargaJual == 0 && tx.Result.Harga > 0 {
		// transaksi.harga = harga_jual di transaksi (langsung pakai)
		hargaJual = int64(tx.Result.Harga)
	}
	totalHarga = hargaBeli + hargaJual
	kodeReseller = tx.Result.KodeReseller
	h.logger.Infof("computed prices ref_id=%s harga_beli=%d harga_jual=%d total_harga=%d kode_reseller=%s", refID, hargaBeli, hargaJual, totalHarga, kodeReseller)

	// Check reseller balance via OtomaX API
	if kodeReseller == "" {
		h.logger.Errorf("kode_reseller is empty for ref_id=%s", refID)
		errorData := map[string]any{
			"ref_id":         refID,
			"buyer_sku_code": productCode,
			"customer_no":    customerNo,
			"status":         "Gagal",
			"message":        "Kode reseller tidak ditemukan",
			"rc":             "40",
		}
		h.respond(w, http.StatusOK, map[string]any{"data": errorData, "message": fmt.Sprintf("Trx ID #%s Kode reseller tidak ditemukan", refID)})
		return
	}

	// Check context timeout before calling GetSaldoRs
	if ctx.Err() != nil {
		h.logger.Errorf("context already cancelled/timeout before GetSaldoRs ref_id=%s kode_reseller=%s err=%v", refID, kodeReseller, ctx.Err())
		errorData := map[string]any{
			"ref_id":         refID,
			"buyer_sku_code": productCode,
			"customer_no":    customerNo,
			"status":         "Gagal",
			"message":        "Timeout saat memeriksa saldo reseller",
			"rc":             "40",
		}
		h.respond(w, http.StatusOK, map[string]any{"data": errorData, "message": fmt.Sprintf("Trx ID #%s Timeout saat memeriksa saldo reseller", refID)})
		return
	}

	saldoInfo, err := h.otomax.GetSaldoRs(ctx, kodeReseller)
	if err != nil {
		// Check if error is due to context timeout
		errType := "unknown"
		if ctx.Err() == context.DeadlineExceeded {
			errType = "timeout (context deadline exceeded)"
		} else if ctx.Err() == context.Canceled {
			errType = "context cancelled"
		} else if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline") {
			errType = "timeout"
		} else if strings.Contains(err.Error(), "connection") || strings.Contains(err.Error(), "network") {
			errType = "network error"
		}
		
		h.logger.Errorf("otomax GetSaldoRs error ref_id=%s kode_reseller=%s error_type=%s err=%v", refID, kodeReseller, errType, err)
		errorData := map[string]any{
			"ref_id":         refID,
			"buyer_sku_code": productCode,
			"customer_no":    customerNo,
			"status":         "Gagal",
			"message":        "Gagal memeriksa saldo reseller",
			"rc":             "40",
		}
		h.respond(w, http.StatusOK, map[string]any{"data": errorData, "message": fmt.Sprintf("Trx ID #%s Gagal memeriksa saldo reseller", refID)})
		return
	}

	// Double-check context after GetSaldoRs (in case it took too long)
	if ctx.Err() != nil {
		h.logger.Errorf("context cancelled/timeout after GetSaldoRs ref_id=%s kode_reseller=%s err=%v", refID, kodeReseller, ctx.Err())
		errorData := map[string]any{
			"ref_id":         refID,
			"buyer_sku_code": productCode,
			"customer_no":    customerNo,
			"status":         "Gagal",
			"message":        "Timeout saat memeriksa saldo reseller",
			"rc":             "40",
		}
		h.respond(w, http.StatusOK, map[string]any{"data": errorData, "message": fmt.Sprintf("Trx ID #%s Timeout saat memeriksa saldo reseller", refID)})
		return
	}

	if saldoInfo == nil || !saldoInfo.OK {
		h.logger.Errorf("otomax GetSaldoRs returned invalid result ref_id=%s kode_reseller=%s saldoInfo=%v", refID, kodeReseller, saldoInfo)
		errorData := map[string]any{
			"ref_id":         refID,
			"buyer_sku_code": productCode,
			"customer_no":    customerNo,
			"status":         "Gagal",
			"message":        "Data reseller tidak ditemukan",
			"rc":             "40",
		}
		h.respond(w, http.StatusOK, map[string]any{"data": errorData, "message": fmt.Sprintf("Trx ID #%s Data reseller tidak ditemukan", refID)})
		return
	}

	saldo := int64(saldoInfo.Result.Saldo)
	saldoMinimal := int64(saldoInfo.Result.SaldoMinimal)
	available := saldo - saldoMinimal
	if available < 0 {
		available = 0
	}
	// Validasi terhadap jumlah yang benar-benar didebit dari saldo reseller (biasanya harga_beli/cost), bukan hanya total_harga
	amountToDebit := hargaBeli
	if totalHarga > amountToDebit {
		amountToDebit = totalHarga
	}
	if available < amountToDebit {
		h.logger.Errorf("insufficient reseller balance ref_id=%s kode_reseller=%s available=%d amount_to_debit=%d (harga_beli=%d total_harga=%d)", refID, kodeReseller, available, amountToDebit, hargaBeli, totalHarga)
		errorData := map[string]any{
			"ref_id":         refID,
			"buyer_sku_code": productCode,
			"customer_no":    customerNo,
			"status":         "Gagal",
			"message":        fmt.Sprintf("Saldo reseller tidak cukup (tersedia: %d, dibutuhkan: %d)", available, amountToDebit),
			"rc":             "40",
		}
		h.respond(w, http.StatusOK, map[string]any{"data": errorData, "message": fmt.Sprintf("Trx ID #%s Saldo reseller tidak cukup", refID)})
		return
	}

	// Save payment transaction to SQLite before processing (for double request detection)
	paymentTx := &domain.Transaction{
		RefID:           refID,
		Action:          domain.ActionPayment,
		ProductCode:     productCode,
		CustomerNo:      customerNo,
		BillAmount:      hargaBeli,
		AdminFee:        h.cfg.DefaultAdminFee,
		Margin:          h.cfg.DefaultMargin,
		SellingPrice:    totalHarga,
		ExternalStatus:  "Process",
		ExternalMessage: "Transaksi sedang diproses",
	}
	if err := h.sqlite.UpsertInquiry(ctx, paymentTx); err != nil {
		h.logger.Errorf("failed to save payment transaction to SQLite ref_id=%s err=%v", refID, err)
		// Continue with payment even if SQLite save fails
	}

	// Async payment: run in background, respond with inquiry data marked Process
	// Pass kodeReseller, totalHarga, and hargaBeli for re-validation and status update
	go func(refID, productCode, customerNo, kodeReseller string, totalHarga, hargaBeli int64) {
		// background context with generous timeout
		bgCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		// Re-validate saldo before payment to prevent negative balance
		// Use shorter timeout for saldo check (5 seconds) to fail fast
		saldoCtx, saldoCancel := context.WithTimeout(bgCtx, 5*time.Second)
		defer saldoCancel()

		saldoInfo, err := h.otomax.GetSaldoRs(saldoCtx, kodeReseller)
		if err != nil {
			// If saldo check fails (timeout, network error, etc), DO NOT proceed with payment
			errType := "unknown"
			if saldoCtx.Err() == context.DeadlineExceeded {
				errType = "timeout"
			} else if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline") {
				errType = "timeout"
			} else if strings.Contains(err.Error(), "connection") || strings.Contains(err.Error(), "network") {
				errType = "network error"
			}
			h.logger.Errorf("background saldo validation failed ref_id=%s kode_reseller=%s error_type=%s err=%v - payment cancelled to prevent negative balance", refID, kodeReseller, errType, err)
			
			// Update transaction status to failed
			failedTx := &domain.Transaction{
				RefID:           refID,
				Action:          domain.ActionPayment,
				ProductCode:     productCode,
				CustomerNo:      customerNo,
				BillAmount:      hargaBeli,
				AdminFee:        h.cfg.DefaultAdminFee,
				Margin:          h.cfg.DefaultMargin,
				SellingPrice:    totalHarga,
				ExternalStatus:  "Gagal",
				ExternalMessage: "Gagal memvalidasi saldo sebelum payment",
			}
			if err := h.sqlite.UpsertInquiry(bgCtx, failedTx); err != nil {
				h.logger.Errorf("failed to update payment status to failed ref_id=%s err=%v", refID, err)
			}
			return
		}

		// Check if saldo is still sufficient
		if saldoInfo == nil || !saldoInfo.OK {
			h.logger.Errorf("background saldo validation returned invalid result ref_id=%s kode_reseller=%s - payment cancelled", refID, kodeReseller)
			
			// Update transaction status to failed
			failedTx := &domain.Transaction{
				RefID:           refID,
				Action:          domain.ActionPayment,
				ProductCode:     productCode,
				CustomerNo:      customerNo,
				BillAmount:      hargaBeli,
				AdminFee:        h.cfg.DefaultAdminFee,
				Margin:          h.cfg.DefaultMargin,
				SellingPrice:    totalHarga,
				ExternalStatus:  "Gagal",
				ExternalMessage: "Data reseller tidak valid",
			}
			if err := h.sqlite.UpsertInquiry(bgCtx, failedTx); err != nil {
				h.logger.Errorf("failed to update payment status to failed ref_id=%s err=%v", refID, err)
			}
			return
		}

		saldo := int64(saldoInfo.Result.Saldo)
		saldoMinimal := int64(saldoInfo.Result.SaldoMinimal)
		available := saldo - saldoMinimal
		if available < 0 {
			available = 0
		}
		amountToDebit := hargaBeli
		if totalHarga > amountToDebit {
			amountToDebit = totalHarga
		}
		if available < amountToDebit {
			h.logger.Errorf("background saldo validation insufficient balance ref_id=%s kode_reseller=%s available=%d amount_to_debit=%d (harga_beli=%d total_harga=%d) - payment cancelled", refID, kodeReseller, available, amountToDebit, hargaBeli, totalHarga)
			
			// Update transaction status to failed
			failedTx := &domain.Transaction{
				RefID:           refID,
				Action:          domain.ActionPayment,
				ProductCode:     productCode,
				CustomerNo:      customerNo,
				BillAmount:      hargaBeli,
				AdminFee:        h.cfg.DefaultAdminFee,
				Margin:          h.cfg.DefaultMargin,
				SellingPrice:    totalHarga,
				ExternalStatus:  "Gagal",
				ExternalMessage: fmt.Sprintf("Saldo reseller tidak cukup (tersedia: %d, dibutuhkan: %d)", available, amountToDebit),
			}
			if err := h.sqlite.UpsertInquiry(bgCtx, failedTx); err != nil {
				h.logger.Errorf("failed to update payment status to failed ref_id=%s err=%v", refID, err)
			}
			return
		}

		// Saldo is sufficient, proceed with payment
		h.logger.Infof("background saldo validation passed ref_id=%s kode_reseller=%s available=%d amount_to_debit=%d - proceeding with payment", refID, kodeReseller, available, amountToDebit)
		payRes, err := h.dgf.Payment(bgCtx, productCode, customerNo, refID)
		if err != nil {
			h.logger.Errorf("digiflazz payment error ref_id=%s err=%v", refID, err)
			return
		}

		// Build friendly success message with optional reff from data.sn
		var cbMessage string
		var nilaiTagihan string
		var lembarTagihan string
		var totalTagihan string
		if strings.EqualFold(payRes.Status, "sukses") || strings.EqualFold(payRes.Status, "success") || strings.EqualFold(payRes.Status, "berhasil") || strings.EqualFold(payRes.Status, "ok") {
			var cno, cname, dstr, reff string
			var adminValue float64
			if rawData, ok := payRes.Raw["data"].(map[string]any); ok {
				if v, ok := rawData["customer_no"].(string); ok {
					cno = v
				}
				if v, ok := rawData["customer_name"].(string); ok {
					cname = v
				}
				if dv, ok := rawData["desc"]; ok {
					dstr = stringifyDesc(dv)
				}
				if v, ok := rawData["sn"].(string); ok {
					reff = v
				}
				// Extract admin from data.admin (top level)
				if admin, ok := rawData["admin"].(float64); ok {
					adminValue = admin
				} else if admin, ok := rawData["admin"].(int64); ok {
					adminValue = float64(admin)
				}
				// Extract selling_price from top level and use it as total_tagihan
				// selling_price already includes all charges (nilai_tagihan + admin + denda) for all lembar_tagihan
				if sp, ok := rawData["selling_price"].(float64); ok {
					totalTagihan = fmt.Sprintf("%.0f", sp)
				} else if sp, ok := rawData["selling_price"].(int64); ok {
					totalTagihan = fmt.Sprintf("%d", sp)
				} else if sp, ok := rawData["selling_price"].(int); ok {
					totalTagihan = fmt.Sprintf("%d", sp)
				} else if sp, ok := rawData["selling_price"].(string); ok {
					totalTagihan = sp
				}
				// Extract nilai_tagihan and lembar_tagihan from desc
				if desc, ok := rawData["desc"]; ok {
					if descMap, ok := desc.(map[string]any); ok {
						// Extract lembar_tagihan from desc.lembar_tagihan
						if lt, ok := descMap["lembar_tagihan"].(float64); ok {
							lembarTagihan = fmt.Sprintf("%.0f", lt)
						} else if lt, ok := descMap["lembar_tagihan"].(int64); ok {
							lembarTagihan = fmt.Sprintf("%d", lt)
						} else if lt, ok := descMap["lembar_tagihan"].(int); ok {
							lembarTagihan = fmt.Sprintf("%d", lt)
						} else if lt, ok := descMap["lembar_tagihan"].(string); ok {
							lembarTagihan = lt
						}
						// Fallback: try to convert if it's a number stored as interface{}
						if lembarTagihan == "" {
							if ltVal := descMap["lembar_tagihan"]; ltVal != nil {
								switch v := ltVal.(type) {
								case float64:
									lembarTagihan = fmt.Sprintf("%.0f", v)
								case int64:
									lembarTagihan = fmt.Sprintf("%d", v)
								case int:
									lembarTagihan = fmt.Sprintf("%d", v)
								case string:
									lembarTagihan = v
								}
							}
						}
						// Extract nilai_tagihan and admin from desc.detail
						if detail, ok := descMap["detail"]; ok {
							if detailArr, ok := detail.([]any); ok && len(detailArr) > 0 {
								// Parse lembar_tagihan to check if > 1
								var lembarTagihanInt int
								if lembarTagihan != "" {
									if lt, err := strconv.Atoi(lembarTagihan); err == nil {
										lembarTagihanInt = lt
									} else if lt, err := strconv.ParseFloat(lembarTagihan, 64); err == nil {
										lembarTagihanInt = int(lt)
									}
								}

								// If lembar_tagihan > 1, combine all nilai_tagihan with # separator
								if lembarTagihanInt > 1 {
									nilaiTagihanParts := make([]string, 0, len(detailArr))
									for _, detailItem := range detailArr {
										if detailMap, ok := detailItem.(map[string]any); ok {
											var ntStr string
											if nt, ok := detailMap["nilai_tagihan"].(string); ok {
												ntStr = nt
											} else if nt, ok := detailMap["nilai_tagihan"].(float64); ok {
												ntStr = fmt.Sprintf("%.0f", nt)
											} else if nt, ok := detailMap["nilai_tagihan"].(int64); ok {
												ntStr = fmt.Sprintf("%d", nt)
											} else if nt, ok := detailMap["nilai_tagihan"].(int); ok {
												ntStr = fmt.Sprintf("%d", nt)
											}
											if ntStr != "" {
												nilaiTagihanParts = append(nilaiTagihanParts, ntStr)
											}
										}
									}
									if len(nilaiTagihanParts) > 0 {
										nilaiTagihan = strings.Join(nilaiTagihanParts, "#")
									}
								} else {
									// Single lembar_tagihan: extract from detail[0] only
									if firstDetail, ok := detailArr[0].(map[string]any); ok {
										// Extract nilai_tagihan
										if nt, ok := firstDetail["nilai_tagihan"].(string); ok {
											nilaiTagihan = nt
										} else if nt, ok := firstDetail["nilai_tagihan"].(float64); ok {
											nilaiTagihan = fmt.Sprintf("%.0f", nt)
										} else if nt, ok := firstDetail["nilai_tagihan"].(int64); ok {
											nilaiTagihan = fmt.Sprintf("%d", nt)
										}
									}
								}

								// Extract admin and denda from detail[0] (for reference only)
								// Get firstDetail for admin/denda extraction
								if firstDetail, ok := detailArr[0].(map[string]any); ok {
									// Extract admin from detail[0].admin if not already found from top level
									if adminValue == 0 {
										if admin, ok := firstDetail["admin"].(string); ok {
											if f, err := strconv.ParseFloat(admin, 64); err == nil {
												adminValue = f
											}
										} else if admin, ok := firstDetail["admin"].(float64); ok {
											adminValue = admin
										} else if admin, ok := firstDetail["admin"].(int64); ok {
											adminValue = float64(admin)
										}
									}
									// Note: denda extraction removed as it's not used for total_tagihan calculation
								}
								// Note: total_tagihan is set from selling_price (top level) to handle multiple lembar_tagihan correctly
							}
						}
					}
				}
				// Fallback: if nilai_tagihan is not found, use selling_price from top level
				if nilaiTagihan == "" {
					if sp, ok := rawData["selling_price"].(float64); ok {
						nilaiTagihan = fmt.Sprintf("%.0f", sp)
					} else if sp, ok := rawData["selling_price"].(int64); ok {
						nilaiTagihan = fmt.Sprintf("%d", sp)
					} else if sp, ok := rawData["selling_price"].(int); ok {
						nilaiTagihan = fmt.Sprintf("%d", sp)
					} else if sp, ok := rawData["selling_price"].(string); ok {
						nilaiTagihan = sp
					}
				}
			}
			cbMessage = fmt.Sprintf("Trx ID #%s Pembayaran %s berhasil SN: customer_no=%s, customer_name=%s%s%s", refID, productCode, cno, cname, prefixIfContent(", ", dstr), prefixIfContent(", reff=", reff))
		} else {
			cbMessage = payRes.Message
		}
		// update sqlite status for audit, save raw data and callback message for message consistency
		// IMPORTANT: Save the exact message that will be sent in callback to ensure consistency
		// IMPORTANT: Save to SQLite FIRST before attempting callback, so double request can work even if callback fails
		h.logger.Infof("saving callback message to SQLite ref_id=%s message=%s", refID, cbMessage)
		if err := h.sqlite.UpdatePayment(bgCtx, refID, string(mapStatus(payRes.Status)), cbMessage, payRes.Raw); err != nil {
			h.logger.Errorf("failed to save callback message to SQLite ref_id=%s err=%v", refID, err)
			// Continue with callback even if SQLite save fails
		}
		// optional callback to OtomaX if configured
		if h.cfg.OtomaxCallbackURL != "" {
			// Build query string manually
			// Message needs special encoding: # as %23, space as %20, & as %26, % as %25
			// Other parameters use standard url.QueryEscape
			params := make([]string, 0, 6)
			params = append(params, "ref_id="+url.QueryEscape(refID))
			params = append(params, "status="+url.QueryEscape(payRes.Status))
			// Message: use custom encoding (encodes #, space, &, % but keeps = as-is)
			params = append(params, "message="+encodeMessageForQuery(cbMessage))
			if nilaiTagihan != "" {
				params = append(params, "nilai_tagihan="+url.QueryEscape(nilaiTagihan))
			}
			if lembarTagihan != "" {
				params = append(params, "lembar_tagihan="+url.QueryEscape(lembarTagihan))
			} else if h.cfg.IsDevelopment() {
				fmt.Printf("[DEBUG] lembar_tagihan is empty for ref_id=%s\n", refID)
			}
			if totalTagihan != "" {
				params = append(params, "total_tagihan="+url.QueryEscape(totalTagihan))
			}
			queryString := strings.Join(params, "&")

			// Parse base URL to get components
			baseURL, err := url.Parse(h.cfg.OtomaxCallbackURL)
			if err != nil {
				h.logger.Errorf("otomax callback URL parse error ref_id=%s url=%s err=%v", refID, h.cfg.OtomaxCallbackURL, err)
			} else {
				// Use url.URL struct with RawQuery to prevent double encoding
				// RawQuery is used as-is without encoding by url.String()
				callbackURLObj := &url.URL{
					Scheme:   baseURL.Scheme,
					Host:     baseURL.Host,
					Path:     baseURL.Path,
					RawQuery: queryString, // Set RawQuery directly to avoid encoding
				}
				callbackURL := callbackURLObj.String()
				h.logger.Infof("sending otomax callback ref_id=%s status=%s message=%s url=%s", refID, payRes.Status, cbMessage, callbackURL)

				// Use callback-specific timeout (default 30 seconds, longer than normal request timeout)
				callbackTimeoutMs := h.cfg.CallbackTimeoutMs
				if callbackTimeoutMs <= 0 {
					// Fallback to 30 seconds if not configured
					callbackTimeoutMs = 30000
				}
				callbackTimeout := time.Duration(callbackTimeoutMs) * time.Millisecond

				// Calculate transport timeouts based on callback timeout
				// Simplified approach: Give server maximum time to respond
				// Connection setup should be quick, so use minimal timeouts for dial/TLS
				// ResponseHeaderTimeout gets most of the time - this is where server processes

				// Dial and TLS timeouts: minimal but reasonable (connection should be quick)
				dialTimeout := 5 * time.Second
				tlsTimeout := 5 * time.Second

				// Response header timeout: Give server almost all the callback timeout
				// This is the critical timeout - server needs time to process and respond
				// Reserve only minimal time for connection setup (dial + TLS + small buffer)
				responseHeaderTimeout := callbackTimeout - dialTimeout - tlsTimeout - 1*time.Second

				// Ensure minimum response header timeout
				if responseHeaderTimeout < 10*time.Second {
					responseHeaderTimeout = 10 * time.Second
				}

				// Log timeout configuration for debugging
				h.logger.Infof("otomax callback timeout config ref_id=%s client_timeout=%v dial_timeout=%v tls_timeout=%v response_header_timeout=%v",
					refID, callbackTimeout, dialTimeout, tlsTimeout, responseHeaderTimeout)

				// Create HTTP client with proper timeout settings for callback
				// Best practice: use separate transport with appropriate timeouts
				callbackClient := &http.Client{
					Timeout: callbackTimeout,
					Transport: &http.Transport{
						DialContext: (&net.Dialer{
							Timeout:   dialTimeout,
							KeepAlive: 30 * time.Second,
						}).DialContext,
						MaxIdleConns:          10,
						IdleConnTimeout:       90 * time.Second,
						TLSHandshakeTimeout:   tlsTimeout,
						ResponseHeaderTimeout: responseHeaderTimeout,
						ExpectContinueTimeout: 1 * time.Second,
						// Disable connection reuse for callbacks to avoid stale connections
						DisableKeepAlives: false,
					},
				}

				// Create request with timeout context
				callbackCtx, callbackCancel := context.WithTimeout(bgCtx, callbackTimeout)
				defer callbackCancel()

				// Create request using url.URL struct directly to prevent double encoding
				// This ensures RawQuery is used as-is without re-encoding
				req := &http.Request{
					Method: http.MethodGet,
					URL:    callbackURLObj,
					Header: make(http.Header),
				}
				req = req.WithContext(callbackCtx)

				// Add User-Agent header (browsers always send this)
				req.Header.Set("User-Agent", "Digiflazz-API/1.0")
				// Add Accept header
				req.Header.Set("Accept", "*/*")
				// Disable keep-alive for callbacks (like browser closing connection)
				req.Header.Set("Connection", "close")

				// Validate request
				if req.URL == nil {
					h.logger.Errorf("otomax callback request creation error ref_id=%s url=%s err=invalid URL", refID, callbackURL)
				} else {

					startTime := time.Now()
					resp, err := callbackClient.Do(req)
					duration := time.Since(startTime)
					if err != nil {
						// Detailed error logging with error type detection
						errMsg := err.Error()
						errType := "unknown"

						// Check for timeout errors
						if ctxErr := callbackCtx.Err(); ctxErr == context.DeadlineExceeded {
							errType = "timeout (context deadline exceeded)"
							h.logger.Errorf("otomax callback timeout ref_id=%s url=%s timeout_ms=%d duration=%v err=%v", refID, callbackURL, callbackTimeoutMs, duration, err)
						} else if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline") {
							errType = "timeout"
							h.logger.Errorf("otomax callback timeout ref_id=%s url=%s timeout_ms=%d duration=%v err=%v", refID, callbackURL, callbackTimeoutMs, duration, err)
						} else if strings.Contains(errMsg, "EOF") || strings.Contains(errMsg, "connection reset") {
							errType = "connection closed"
							h.logger.Errorf("otomax callback connection closed ref_id=%s url=%s duration=%v err=%v (server may have closed connection)", refID, callbackURL, duration, err)
						} else if strings.Contains(errMsg, "no such host") || strings.Contains(errMsg, "network is unreachable") {
							errType = "network error"
							h.logger.Errorf("otomax callback network error ref_id=%s url=%s duration=%v err=%v", refID, callbackURL, duration, err)
						} else {
							h.logger.Errorf("otomax callback error ref_id=%s url=%s duration=%v err=%v", refID, callbackURL, duration, err)
						}

						h.logger.Errorf("otomax callback failed ref_id=%s error_type=%s duration=%v timeout_ms=%d (OtomaX should re-hit to get status)", refID, errType, duration, callbackTimeoutMs)
					} else {
						// Always close response body to prevent resource leak
						defer func() {
							// Discard remaining body to allow connection reuse
							io.Copy(io.Discard, resp.Body)
							resp.Body.Close()
						}()

						// Read response body for debugging (limit to 1KB to avoid memory issues)
						bodyBytes := make([]byte, 1024)
						n, readErr := io.ReadFull(io.LimitReader(resp.Body, 1024), bodyBytes)
						if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
							// If read fails, try simple read
							n, _ = resp.Body.Read(bodyBytes)
						}
						bodyPreview := string(bodyBytes[:n])
						if n == 1024 {
							bodyPreview += "... (truncated)"
						}

						if resp.StatusCode >= 200 && resp.StatusCode < 300 {
							h.logger.Infof("otomax callback success ref_id=%s status=%s http_status=%d duration=%v response_preview=%s", refID, payRes.Status, resp.StatusCode, duration, bodyPreview)
						} else {
							h.logger.Errorf("otomax callback non-2xx response ref_id=%s status=%s http_status=%d duration=%v response_preview=%s (OtomaX should re-hit to get status)", refID, payRes.Status, resp.StatusCode, duration, bodyPreview)
						}
					}
				}
			}
		} else {
			h.logger.Infof("otomax callback URL not configured, skipping callback for ref_id=%s", refID)
		}
	}(refID, productCode, customerNo, kodeReseller, totalHarga, hargaBeli)

	// Build immediate response from inquiry data with status Process
	pendingData := map[string]any{}
	if inqRaw != nil {
		for k, v := range inqRaw {
			pendingData[k] = v
		}
	} else {
		pendingData["ref_id"] = refID
		pendingData["buyer_sku_code"] = productCode
		pendingData["customer_no"] = customerNo
	}
	pendingData["status"] = "Process"
	pendingData["message"] = "Transaksi sedang diproses"
	pendingData["rc"] = "03"
	// Optional friendly message
	var messageStr string
	{
		var customerName, descStr string
		if v, ok := pendingData["customer_name"].(string); ok {
			customerName = v
		}
		if dv, ok := pendingData["desc"]; ok {
			descStr = stringifyDesc(dv)
		}
		messageStr = fmt.Sprintf("Trx ID #%s Pembayaran diproses SN: customer_no=%s, customer_name=%s%s", refID, customerNo, customerName, prefixIfContent(", ", descStr))
	}
	responseFinal := map[string]any{"data": pendingData, "message": messageStr}
	h.respond(w, http.StatusOK, responseFinal)
}

func (h *OtomaxHandler) respond(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func mapStatus(s string) domain.TransactionStatus {
	switch strings.ToLower(s) {
	case "sukses", "success", "berhasil", "ok":
		return domain.StatusSuccess
	case "pending", "process", "processing":
		return domain.StatusPending
	default:
		return domain.StatusFailed
	}
}

func clientIPFromRequest(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				return p
			}
		}
	}
	if rip := r.Header.Get("X-Real-IP"); rip != "" {
		return strings.TrimSpace(rip)
	}
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return ip
	}
	return r.RemoteAddr
}

// stringifyDesc converts Digiflazz desc payload into a readable single-line string (no JSON)
func stringifyDesc(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		parts := make([]string, 0, 16)
		// Common top-levels
		if val, ok := t["tarif"]; ok {
			parts = append(parts, fmt.Sprintf("tarif=%v", val))
		}
		if val, ok := t["daya"]; ok {
			parts = append(parts, fmt.Sprintf("daya=%v", val))
		}
		if val, ok := t["lembar_tagihan"]; ok {
			parts = append(parts, fmt.Sprintf("lembar_tagihan=%v", val))
		}
		// Detail first item flattened without prefix
		if d, ok := t["detail"]; ok {
			if arr, ok := d.([]any); ok && len(arr) > 0 {
				if first, ok := arr[0].(map[string]any); ok {
					order := []string{"periode", "nilai_tagihan", "admin", "denda", "meter_awal", "meter_akhir", "biaya_lain"}
					for _, k := range order {
						if v, ok := first[k]; ok {
							parts = append(parts, fmt.Sprintf("%s=%v", k, v))
						}
					}
					// include unexpected keys as well
					include := map[string]struct{}{}
					for _, k := range order {
						include[k] = struct{}{}
					}
					for k, v := range first {
						if _, seen := include[k]; seen {
							continue
						}
						parts = append(parts, fmt.Sprintf("%s=%v", k, v))
					}
				}
			}
		}
		// Include other top-level keys (exclude detail and those already added)
		exclude := map[string]struct{}{"tarif": {}, "daya": {}, "lembar_tagihan": {}, "detail": {}}
		for k, val := range t {
			if _, skip := exclude[k]; skip {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s=%v", k, val))
		}
		if len(parts) == 0 {
			return ""
		}
		return strings.Join(parts, ", ")
	case []any:
		// Take first item as representative
		if len(t) == 0 {
			return ""
		}
		return stringifyDesc(t[0])
	default:
		return fmt.Sprintf("%v", t)
	}
}

func prefixIfContent(prefix, content string) string {
	if content == "" {
		return ""
	}
	return prefix + content
}
