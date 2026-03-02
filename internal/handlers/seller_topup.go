package handlers

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"digiflazz-api/internal/config"
	"digiflazz-api/internal/domain"
	"digiflazz-api/internal/logging"
	"digiflazz-api/internal/otomax"
	mssqlstore "digiflazz-api/internal/storage/mssql"
	sqlitestore "digiflazz-api/internal/storage/sqlite"
)

// SellerTopupHandler handles incoming topup requests from DigiFlazz Seller API.
// Validates sign, checks price >= harga_jual, forwards to OtomaX via InsertInbox, responds with DigiFlazz format.
type SellerTopupHandler struct {
	cfg    *config.Config
	logger *logging.Logger
	sqlite *sqlitestore.Store
	mssql  *mssqlstore.Client
	otomax *otomax.Client
}

// NewSellerTopupHandler creates a new SellerTopupHandler.
func NewSellerTopupHandler(cfg *config.Config, logger *logging.Logger, sqlite *sqlitestore.Store, mssql *mssqlstore.Client, otomax *otomax.Client) *SellerTopupHandler {
	return &SellerTopupHandler{cfg: cfg, logger: logger, sqlite: sqlite, mssql: mssql, otomax: otomax}
}

// DigiFlazzSellerTopupRequest is the incoming JSON body from DigiFlazz (Seller topup).
type DigiFlazzSellerTopupRequest struct {
	Username   string `json:"username"`
	Commands   string `json:"commands"`
	RefID      string `json:"ref_id"`
	Hp         string `json:"hp"`
	PulsaCode  string `json:"pulsa_code"`
	Price      int64  `json:"price"`
	Sign       string `json:"sign"`
}

// digiflazzDataResponse is the "data" object we return to DigiFlazz (all fields string per doc).
type digiflazzDataResponse struct {
	RefID   string `json:"ref_id"`
	Status  string `json:"status"`
	Code    string `json:"code"`
	Hp      string `json:"hp"`
	Price   string `json:"price"`
	Message string `json:"message"`
	Balance string `json:"balance"`
	TrID    string `json:"tr_id"`
	Rc      string `json:"rc"`
	Sn      string `json:"sn"`
}

func (h *SellerTopupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req DigiFlazzSellerTopupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Errorf("seller topup decode error: %v", err)
		balanceStr := h.getBalanceForResponse(r.Context(), "")
		h.respondDigiFlazzError(w, "", "", "", "Request tidak valid", "40", balanceStr)
		return
	}

	refID := strings.TrimSpace(req.RefID)
	hp := strings.TrimSpace(req.Hp)
	pulsaCode := strings.TrimSpace(strings.ToLower(req.PulsaCode))
	if refID == "" || hp == "" || pulsaCode == "" {
		h.logger.Errorf("seller topup missing required field ref_id=%s hp=%s pulsa_code=%s", refID, hp, pulsaCode)
		balanceStr := h.getBalanceForResponse(r.Context(), req.Username)
		h.respondDigiFlazzError(w, refID, pulsaCode, hp, "Parameter wajib kosong", "40", balanceStr)
		return
	}

	// 1) Validate sign: md5(username + apiKey + ref_id)
	expectedSign := md5.Sum([]byte(req.Username + h.cfg.DigiflazzAPIKey + refID))
	expectedSignStr := hex.EncodeToString(expectedSign[:])
	if strings.ToLower(req.Sign) != strings.ToLower(expectedSignStr) {
		h.logger.Errorf("seller topup invalid sign ref_id=%s", refID)
		balanceStr := h.getBalanceForResponse(r.Context(), req.Username)
		h.respondDigiFlazzError(w, refID, pulsaCode, hp, "Signature tidak valid", "40", balanceStr)
		return
	}

	ctx := r.Context()

	// 2) Idempotency: same ref_id return existing response
	if h.sqlite != nil {
		existing, err := h.sqlite.GetByRefID(ctx, refID)
		if err == nil && existing != nil && existing.Action == domain.ActionSellerTopup {
			rc := "39"
			if existing.ExternalStatus == "1" {
				rc = "00"
			}
			balanceStr := h.getBalanceForResponse(ctx, req.Username)
			h.logger.Infof("seller topup idempotent ref_id=%s returning existing status=%s", refID, existing.ExternalStatus)
			h.respondDigiFlazzData(w, refID, existing.ProductCode, existing.CustomerNo, existing.SellingPrice, existing.ExternalStatus, existing.ExternalMessage, rc, balanceStr)
			return
		}
	}

	// 3) Validate price >= harga_jual (QueryProduk or MSSQL)
	hargaJual := int64(0)
	if h.otomax != nil {
		if prod, err := h.otomax.QueryProduk(ctx, pulsaCode); err == nil && prod != nil && prod.OK {
			hargaJual = int64(prod.Result.HargaJual)
		}
	}
	if hargaJual == 0 && h.mssql != nil {
		if hj, err := h.mssql.GetProductHargaJualByCode(ctx, pulsaCode); err == nil {
			hargaJual = hj
		}
	}
	if req.Price < hargaJual {
		h.logger.Infof("seller topup price below harga_jual ref_id=%s price=%d harga_jual=%d", refID, req.Price, hargaJual)
		balanceStr := h.getBalanceForResponse(ctx, req.Username)
		h.respondDigiFlazzError(w, refID, pulsaCode, hp, fmt.Sprintf("Harga tidak boleh lebih murah dari harga jual (min %d)", hargaJual), "40", balanceStr)
		return
	}

	// 4) Build pesan from .env format and forward via InsertInbox (kode_reseller = username dari request DigiFlazz)
	kodeReseller := strings.TrimSpace(req.Username)
	if kodeReseller == "" {
		h.logger.Errorf("seller topup username kosong ref_id=%s", refID)
		balanceStr := h.getBalanceForResponse(ctx, req.Username)
		h.respondDigiFlazzError(w, refID, pulsaCode, hp, "Username tidak boleh kosong", "40", balanceStr)
		return
	}

	format := h.cfg.OtomaxInsertInboxPesanFormat
	if format == "" {
		format = "{{pulsa_code}}.{{hp}}.{{price}}.{{ref_id}}"
	}
	values := map[string]string{
		"ref_id":      refID,
		"hp":          hp,
		"pulsa_code":  pulsaCode,
		"price":       strconv.FormatInt(req.Price, 10),
		"username":    req.Username,
		"commands":    req.Commands,
	}
	pesan := otomax.BuildPesanFromTemplate(format, values)

	if h.otomax != nil {
		insertReq := otomax.InsertInboxRequest{
			Pesan:        pesan,
			KodeReseller: kodeReseller,
			Pengirim:     hp,
			TipePengirim: "W",
		}
		if _, err := h.otomax.InsertInbox(ctx, insertReq); err != nil {
			h.logger.Errorf("seller topup InsertInbox error ref_id=%s err=%v", refID, err)
			balanceStr := h.getBalanceForResponse(ctx, req.Username)
			h.respondDigiFlazzError(w, refID, pulsaCode, hp, "Gagal meneruskan ke OtomaX", "40", balanceStr)
			return
		}
		h.logger.Infof("seller topup InsertInbox ok ref_id=%s pesan=%s", refID, pesan)
	}

	// 5) Persist for idempotency and respond pending
	if h.sqlite != nil {
		tx := &domain.Transaction{
			RefID:           refID,
			Action:          domain.ActionSellerTopup,
			ProductCode:     pulsaCode,
			CustomerNo:      hp,
			BillAmount:      req.Price,
			AdminFee:        0,
			Margin:          0,
			SellingPrice:    req.Price,
			ExternalStatus:  "0",
			ExternalMessage: "Process",
		}
		_ = h.sqlite.UpsertInquiry(ctx, tx)
	}

	balanceStr := h.getBalanceForResponse(ctx, req.Username)
	h.respondDigiFlazzData(w, refID, pulsaCode, hp, req.Price, "0", "Process", "39", balanceStr)
}

// getBalanceForResponse returns balance from OtomaX GetSaldoRs(kode_reseller) untuk field balance di response DigiFlazz. Jika gagal atau kode kosong, kembalikan "0".
func (h *SellerTopupHandler) getBalanceForResponse(ctx context.Context, kodeReseller string) string {
	kodeReseller = strings.TrimSpace(kodeReseller)
	if kodeReseller == "" || h.otomax == nil {
		return "0"
	}
	info, err := h.otomax.GetSaldoRs(ctx, kodeReseller)
	if err != nil || info == nil || !info.OK {
		return "0"
	}
	saldo := int64(info.Result.Saldo)
	if saldo < 0 {
		saldo = 0
	}
	return strconv.FormatInt(saldo, 10) // balance untuk response DigiFlazz (dari GetSaldoRs)
}

func (h *SellerTopupHandler) respondDigiFlazzData(w http.ResponseWriter, refID, code, hp string, price int64, status, message, rc, balance string) {
	sn := ""
	if status == "1" {
		sn = "1" // actual sn can be set from callback/flow when status updated
	}
	if balance == "" {
		balance = "0"
	}
	data := digiflazzDataResponse{
		RefID:   refID,
		Status:  status,
		Code:    code,
		Hp:      hp,
		Price:   strconv.FormatInt(price, 10),
		Message: message,
		Balance: balance,
		TrID:    refID,
		Rc:      rc,
		Sn:      sn,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func (h *SellerTopupHandler) respondDigiFlazzError(w http.ResponseWriter, refID, code, hp, message, rc, balance string) {
	if balance == "" {
		balance = "0"
	}
	data := digiflazzDataResponse{
		RefID:   refID,
		Status:  "0",
		Code:    code,
		Hp:      hp,
		Price:   "0",
		Message: message,
		Balance: balance,
		TrID:    refID,
		Rc:      rc,
		Sn:      "",
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}
