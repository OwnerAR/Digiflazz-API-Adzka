package mssql

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/microsoft/go-mssqldb"
)

type Client struct {
	db *sql.DB
}

func New(dsn string) (*Client, error) {
	if dsn == "" {
		return &Client{db: nil}, nil
	}
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, err
	}
	return &Client{db: db}, nil
}

func (c *Client) UpdateTransactionStatus(ctx context.Context, refID, status, message string) error {
	if c.db == nil {
		return nil
	}
	// NOTE: Adjust table name if different. Using OtomaX columns: status, keterangan, ref_id
	_, err := c.db.ExecContext(ctx, `
UPDATE transaksi SET status = @p1, keterangan = @p2 WHERE kode = @p3
`, status, message, refID)
	return err
}

type OtomaxTransaction struct {
	ProductCode string
	CustomerNo  string
}

func (c *Client) GetTransactionByRefID(ctx context.Context, refID string) (*OtomaxTransaction, error) {
	if c.db == nil {
		return nil, nil
	}
	// Using OtomaX columns: kode_produk -> product code, tujuan -> customer number
	// NOTE: Adjust table name if different
	row := c.db.QueryRowContext(ctx, `
SELECT kode_produk, tujuan FROM transaksi WHERE kode = @p1
`, refID)
	var t OtomaxTransaction
	if err := row.Scan(&t.ProductCode, &t.CustomerNo); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (c *Client) PingContext(ctx context.Context) error {
	if c.db == nil {
		return sql.ErrConnDone
	}
	return c.db.PingContext(ctx)
}

// Get list of product codes from OtomaX product table
func (c *Client) GetProductCodes(ctx context.Context) ([]string, error) {
	if c.db == nil {
		return nil, nil
	}
	// Column is 'kode' in OtomaX 'produk' table
	rows, err := c.db.QueryContext(ctx, `SELECT kode FROM produk`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var codes []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, rows.Err()
}

// (removed) InsertParsingRows: replaced by per-item Exists + InsertParsing flow

// ExistsProductByCode checks if a product code exists in produk table
func (c *Client) ExistsProductByCode(ctx context.Context, code string) (bool, error) {
	if c.db == nil {
		return false, nil
	}
	row := c.db.QueryRowContext(ctx, `SELECT 1 FROM produk WHERE kode = @p1`, code)
	var one int
	if err := row.Scan(&one); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// GetProductHargaTetap returns harga_tetap value (0 or 1) from produk table for given code
// Returns 0 (false) if null/empty or not found
func (c *Client) GetProductHargaTetap(ctx context.Context, code string) (int, error) {
	if c.db == nil {
		return 0, nil
	}
	row := c.db.QueryRowContext(ctx, `
SELECT CAST(ISNULL(harga_tetap, 0) AS INT) AS harga_tetap
FROM produk WHERE kode = @p1`, code)
	var hargaTetap sql.NullInt64
	if err := row.Scan(&hargaTetap); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	if hargaTetap.Valid {
		return int(hargaTetap.Int64), nil
	}
	return 0, nil
}

// ExistsParsing checks if a parsing row already exists for kode_modul + kode_produk
func (c *Client) ExistsParsing(ctx context.Context, kodeModul, code string) (bool, error) {
	if c.db == nil {
		return false, nil
	}
	row := c.db.QueryRowContext(ctx, `SELECT 1 FROM parsing WHERE kode_modul = @p1 AND kode_produk = @p2`, kodeModul, code)
	var one int
	if err := row.Scan(&one); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// InsertParsing inserts a single parsing row with given perintah
func (c *Client) InsertParsing(ctx context.Context, kodeModul, code, perintah string, hargaBeli int64, markup int64) error {
	if c.db == nil {
		return nil
	}
	_, err := c.db.ExecContext(ctx, `
INSERT INTO parsing (aktif, kode_modul, kode_produk, perintah, keterangan, harga_beli, markup)
VALUES (1, @p1, @p2, @p3, @p4, @p5, @p6)
`, kodeModul, code, perintah, "Inject Addon", hargaBeli, markup)
	return err
}

// UpdateParsingHargaBeli updates harga_beli in parsing table for given kode_modul and kode_produk
func (c *Client) UpdateParsingHargaBeli(ctx context.Context, kodeModul, code string, hargaBeli int64) error {
	if c.db == nil {
		return nil
	}
	res, err := c.db.ExecContext(ctx, `
UPDATE parsing SET harga_beli = @p1 WHERE kode_modul = @p2 AND kode_produk = @p3
`, hargaBeli, kodeModul, code)
	if err != nil {
		return err
	}
	if rows, rerr := res.RowsAffected(); rerr == nil && rows == 0 {
		return fmt.Errorf("no parsing row updated for kode_modul=%s kode_produk=%s", kodeModul, code)
	}
	return err
}

// GetParsingHarga returns harga_beli and markup from parsing table for given kode_modul and kode_produk
// Returns 0 for harga_beli and markup if null/empty in database
func (c *Client) GetParsingHarga(ctx context.Context, kodeModul, code string) (hargaBeli int64, markup int64, err error) {
	if c.db == nil {
		return 0, 0, nil
	}
	row := c.db.QueryRowContext(ctx, `
SELECT 
  CAST(ROUND(CONVERT(NUMERIC(18,4), ISNULL(harga_beli, 0)), 0) AS BIGINT) AS harga_beli,
  CAST(ROUND(CONVERT(NUMERIC(18,4), ISNULL(markup, 0)), 0) AS BIGINT) AS markup
FROM parsing WHERE kode_modul = @p1 AND kode_produk = @p2`, kodeModul, code)
	var hb, mk sql.NullInt64
	if scanErr := row.Scan(&hb, &mk); scanErr != nil {
		if scanErr == sql.ErrNoRows {
			return 0, 0, nil
		}
		return 0, 0, scanErr
	}
	// harga_beli dan markup default 0 jika null/empty
	if hb.Valid {
		hargaBeli = hb.Int64
	} else {
		hargaBeli = 0
	}
	if mk.Valid {
		markup = mk.Int64
	} else {
		markup = 0
	}
	return
}

// update HargaBeli and HargaJual in produk table
func (c *Client) UpdateProductHarga(ctx context.Context, code string, hargaBeli, hargaJual int64) error {
	if c.db == nil {
		return nil
	}
	_, err := c.db.ExecContext(ctx, `
UPDATE produk SET harga_beli = @p1, harga_jual = @p2 WHERE kode = @p3`, hargaBeli, hargaJual, code)
	return err
}

// GetProductHargaJualByCode returns harga_jual from produk table for a given kode (buyer_sku_code)
func (c *Client) GetProductHargaJualByCode(ctx context.Context, code string) (int64, error) {
	if c.db == nil {
		return 0, nil
	}
	// Convert NUMERIC/VARCHAR to BIGINT (rounded) with wide compatibility
	row := c.db.QueryRowContext(ctx, `
SELECT CAST(ROUND(CONVERT(NUMERIC(18,4), ISNULL(harga_jual, 0)), 0) AS BIGINT)
FROM produk WHERE kode = @p1`, code)
	var v sql.NullInt64
	if err := row.Scan(&v); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return v.Int64, nil
}

// UpdateTransactionPrices sets harga_beli and harga in transaksi for given ref (mapped to transaksi.kode)
func (c *Client) UpdateTransactionPrices(ctx context.Context, refID string, hargaBeli, harga int64) error {
	if c.db == nil {
		return nil
	}
	_, err := c.db.ExecContext(ctx, `
UPDATE transaksi SET harga_beli = @p1, harga = @p2 WHERE kode = @p3
`, hargaBeli, harga, refID)
	return err
}

// GetTransactionHargaByRef returns transaksi.harga (BIGINT) for given ref (transaksi.kode)
func (c *Client) GetTransactionHargaByRef(ctx context.Context, refID string) (int64, error) {
	if c.db == nil {
		return 0, nil
	}
	row := c.db.QueryRowContext(ctx, `
SELECT CAST(ROUND(CONVERT(NUMERIC(18,4), ISNULL(harga, 0)), 0) AS BIGINT)
FROM transaksi WHERE kode = @p1`, refID)
	var v sql.NullInt64
	if err := row.Scan(&v); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return v.Int64, nil
}

// GetTransaksiKodeResellerByRef returns kode_reseller from transaksi for given ref (transaksi.kode)
func (c *Client) GetTransaksiKodeResellerByRef(ctx context.Context, refID string) (string, error) {
	if c.db == nil {
		return "", nil
	}
	row := c.db.QueryRowContext(ctx, `SELECT kode_reseller FROM transaksi WHERE kode = @p1`, refID)
	var kodeReseller string
	if err := row.Scan(&kodeReseller); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return kodeReseller, nil
}

// GetResellerBalanceByKode returns saldo reseller
func (c *Client) GetResellerBalanceAndStatus(ctx context.Context, kodeReseller string) (saldo int64, saldoMinimal int64, aktif int, suspend int, deleted int, err error) {
	if c.db == nil {
		return 0, 0, 0, 0, 0, nil
	}
	row := c.db.QueryRowContext(ctx, `
SELECT 
  CAST(ROUND(CONVERT(NUMERIC(18,4), ISNULL(saldo, 0)), 0) AS BIGINT) AS saldo,
  CAST(ROUND(CONVERT(NUMERIC(18,4), ISNULL(saldo_minimal, 0)), 0) AS BIGINT) AS saldo_minimal,
  CAST(ISNULL(aktif, 0) AS INT) AS aktif,
  CAST(ISNULL(suspend, 0) AS INT) AS suspend,
  CAST(ISNULL(deleted, 0) AS INT) AS deleted
FROM reseller WHERE kode = @p1`, kodeReseller)
	if scanErr := row.Scan(&saldo, &saldoMinimal, &aktif, &suspend, &deleted); scanErr != nil {
		if scanErr == sql.ErrNoRows {
			return 0, 0, 0, 0, 0, nil
		}
		return 0, 0, 0, 0, 0, scanErr
	}
	return
}

// UpdateTransactionSN sets the sn column in transaksi (by kode = refID)
func (c *Client) UpdateTransactionSN(ctx context.Context, refID string, sn string) error {
	if c.db == nil {
		return nil
	}
	_, err := c.db.ExecContext(ctx, `UPDATE transaksi SET sn = @p1 WHERE kode = @p2`, sn, refID)
	return err
}
