package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"digiflazz-api/internal/config"
	"digiflazz-api/internal/digiflazz"
	"digiflazz-api/internal/logging"
	mssqlstore "digiflazz-api/internal/storage/mssql"
)

type ProductSyncHandler struct {
	cfg    *config.Config
	logger *logging.Logger
	mssql  *mssqlstore.Client
	dgf    *digiflazz.Client
}

func NewProductSyncHandler(cfg *config.Config, logger *logging.Logger, mssql *mssqlstore.Client, dgf *digiflazz.Client) *ProductSyncHandler {
	return &ProductSyncHandler{cfg: cfg, logger: logger, mssql: mssql, dgf: dgf}
}

func RunProductSync(ctx context.Context, cfg *config.Config, logger *logging.Logger, mssql *mssqlstore.Client, dgf *digiflazz.Client) (int, int, int, error) {
	items, err := dgf.GetPriceListPasca(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	// Count total produk in OtomaX for reporting
	codes, err := mssql.GetProductCodes(ctx)
	if err != nil {
		return 0, 0, 0, err
	}

	if cfg.OtomaxKodeModul == "" {
		return len(items), len(codes), 0, nil
	}
	inserted := 0
	for _, it := range items {
		code := strings.TrimSpace(it.BuyerSKUCode)
		if code == "" {
			continue
		}
		// 1) cek produk di tabel produk
		ok, err := mssql.ExistsProductByCode(ctx, code)
		if err != nil {
			return 0, 0, 0, err
		}
		if !ok {
			continue
		}
		// 2) cek parsing sudah ada
		has, err := mssql.ExistsParsing(ctx, cfg.OtomaxKodeModul, code)
		if err != nil {
			return 0, 0, 0, err
		}
		if has {
			continue
		}
		// 3) insert parsing (pasca tidak perlu update harga)
		if err := mssql.InsertParsing(ctx, cfg.OtomaxKodeModul, code, "?action=payment&ref_id=[trxid]", 0, 0); err != nil {
			return 0, 0, 0, err
		}
		inserted++
	}
	return len(items), len(codes), inserted, nil
}

func (h *ProductSyncHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	totalPricelist, totalProducts, inserted, err := RunProductSync(ctx, h.cfg, h.logger, h.mssql, h.dgf)
	if err != nil {
		http.Error(w, "sync failed", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"total_pricelist":       totalPricelist,
		"total_otomax_products": totalProducts,
		"inserted_parsing":      inserted,
	})
}

// Prepaid sync
type ProductPrepaidSyncHandler struct {
	cfg    *config.Config
	logger *logging.Logger
	mssql  *mssqlstore.Client
	dgf    *digiflazz.Client
}

func NewProductPrepaidSyncHandler(cfg *config.Config, logger *logging.Logger, mssql *mssqlstore.Client, dgf *digiflazz.Client) *ProductPrepaidSyncHandler {
	return &ProductPrepaidSyncHandler{cfg: cfg, logger: logger, mssql: mssql, dgf: dgf}
}

func RunProductSyncPrepaid(ctx context.Context, cfg *config.Config, logger *logging.Logger, mssql *mssqlstore.Client, dgf *digiflazz.Client) (int, int, int, error) {
	items, err := dgf.GetPriceListPrepaid(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	codes, err := mssql.GetProductCodes(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	exists := make(map[string]struct{}, len(codes))
	for _, c := range codes {
		cc := strings.TrimSpace(c)
		if cc == "" {
			continue
		}
		exists[cc] = struct{}{}
	}
	inserted := 0
	for _, it := range items {
		code := strings.TrimSpace(it.BuyerSKUCode)
		if code == "" {
			continue
		}
		if _, ok := exists[code]; !ok {
			continue
		}
		has, err := mssql.ExistsParsing(ctx, cfg.OtomaxKodeModul, code)
		if err != nil {
			return 0, 0, 0, err
		}

		// Ambil harga dari Digiflazz (field price)
		var hargaBeliFromDigiflazz int64
		if it.Price > 0 {
			hargaBeliFromDigiflazz = int64(it.Price)
		}

		if !has {
			// Insert parsing jika belum ada, dengan harga_beli dari Digiflazz
			if err := mssql.InsertParsing(ctx, cfg.OtomaxKodeModul, code, "/prepaid?ref_id=[trxid]", hargaBeliFromDigiflazz, cfg.DefaultMarkup); err != nil {
				return 0, 0, 0, err
			}
			inserted++
		} else {
			// Update harga_beli di parsing dari harga Digiflazz jika ada
			if hargaBeliFromDigiflazz > 0 {
				if err := mssql.UpdateParsingHargaBeli(ctx, cfg.OtomaxKodeModul, code, hargaBeliFromDigiflazz); err != nil {
					logger.Errorf("failed to update parsing harga_beli for code=%s: %v", code, err)
					// Continue to next item, don't fail entire sync
				}
			}
		}

		// Jika konfigurasi menonaktifkan update harga produk, lanjut ke item berikutnya
		if !cfg.UpdateProductPrice {
			continue
		}

		// Cek harga_tetap dari produk: jika 1, skip update harga
		hargaTetap, err := mssql.GetProductHargaTetap(ctx, code)
		if err != nil {
			return 0, 0, 0, err
		}
		if hargaTetap == 1 {
			// Produk dengan harga_tetap = 1 tidak boleh diupdate harga
			continue
		}

		// Ambil harga dari parsing (sudah di-update dari Digiflazz di atas)
		// markup akan default 0 jika null/kosong di database
		hargaBeli, markup, err := mssql.GetParsingHarga(ctx, cfg.OtomaxKodeModul, code)
		if err != nil {
			return 0, 0, 0, err
		}

		// Hanya update harga jika harga_beli > 0 (harga sudah ada di parsing)
		// Jika harga_beli = 0, berarti harga belum di-set di parsing, skip update
		if hargaBeli <= 0 {
			// Harga belum tersedia di parsing, skip update
			continue
		}
		// Jika markup <= 0, skip update
		if markup <= 0 {
			continue
		}
		// Update harga produk: harga_beli dari parsing, harga_jual = harga_beli + markup
		// Contoh: harga_beli=3000, markup=0 → harga_jual=3000+0=3000
		// Contoh: harga_beli=3000, markup=500 → harga_jual=3000+500=3500
		// jika markup 1 - 10, maka markup = harga_beli * markup / 100
		if markup >= 1 && markup <= 5 {
			markup = hargaBeli * markup / 100
		}
		hargaJual := hargaBeli + markup
		if err := mssql.UpdateProductHarga(ctx, code, hargaBeli, hargaJual); err != nil {
			logger.Errorf("failed to update product harga for code=%s: %v", code, err)
			// Continue to next item, don't fail entire sync
		}
		logger.Infof("updated product harga for code=%s: harga_beli=%d, harga_jual=%d", code, hargaBeli, hargaJual)
	}
	return len(items), len(codes), inserted, nil
}

func (h *ProductPrepaidSyncHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	totalPricelist, totalProducts, inserted, err := RunProductSyncPrepaid(ctx, h.cfg, h.logger, h.mssql, h.dgf)
	if err != nil {
		http.Error(w, "sync failed", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"total_pricelist":       totalPricelist,
		"total_otomax_products": totalProducts,
		"inserted_parsing":      inserted,
	})
}
