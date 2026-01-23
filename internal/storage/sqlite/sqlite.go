package sqlite

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "time"

    _ "modernc.org/sqlite"

    "digiflazz-api/internal/domain"
)

type Store struct {
    db *sql.DB
}

func New(dsn string) (*Store, error) {
    db, err := sql.Open("sqlite", dsn)
    if err != nil {
        return nil, err
    }
    db.SetMaxOpenConns(1)
    s := &Store{db: db}
    if err := s.migrate(); err != nil {
        return nil, err
    }
    return s, nil
}

func (s *Store) migrate() error {
    schema := `
CREATE TABLE IF NOT EXISTS transactions (
    ref_id TEXT PRIMARY KEY,
    action TEXT NOT NULL,
    product_code TEXT NOT NULL,
    customer_no TEXT NOT NULL,
    bill_amount INTEGER NOT NULL,
    admin_fee INTEGER NOT NULL,
    margin INTEGER NOT NULL,
    selling_price INTEGER NOT NULL,
    external_status TEXT,
    external_message TEXT,
    raw_data TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS prepaid_cache (
    customer_no TEXT NOT NULL,
    product_code TEXT NOT NULL,
    raw_data TEXT NOT NULL,
    sn TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (customer_no, product_code)
);
`
    if _, err := s.db.Exec(schema); err != nil {
        return err
    }
    // Add raw_data column if it doesn't exist (for existing databases)
    // SQLite doesn't support IF NOT EXISTS for ALTER TABLE, so we check first
    rows, err := s.db.Query("PRAGMA table_info(transactions)")
    if err == nil {
        defer rows.Close()
        hasRawData := false
        for rows.Next() {
            var cid int
            var name, ctype string
            var notnull int
            var dfltValue sql.NullString
            var pk int
            if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err == nil {
                if name == "raw_data" {
                    hasRawData = true
                    break
                }
            }
        }
        if !hasRawData {
            // Column doesn't exist, add it
            if _, err := s.db.Exec("ALTER TABLE transactions ADD COLUMN raw_data TEXT"); err != nil {
                // Ignore error if column already exists (race condition)
                fmt.Printf("warning: failed to add raw_data column (may already exist): %v\n", err)
            }
        }
    }
    return nil
}

func (s *Store) UpsertInquiry(ctx context.Context, t *domain.Transaction) error {
    now := time.Now()
    t.CreatedAt = now
    t.UpdatedAt = now
    _, err := s.db.ExecContext(ctx, `
INSERT INTO transactions (
    ref_id, action, product_code, customer_no, bill_amount, admin_fee, margin, selling_price, external_status, external_message, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(ref_id) DO UPDATE SET
    action=excluded.action,
    product_code=excluded.product_code,
    customer_no=excluded.customer_no,
    bill_amount=excluded.bill_amount,
    admin_fee=excluded.admin_fee,
    margin=excluded.margin,
    selling_price=excluded.selling_price,
    external_status=excluded.external_status,
    external_message=excluded.external_message,
    updated_at=excluded.updated_at
`, t.RefID, string(t.Action), t.ProductCode, t.CustomerNo, t.BillAmount, t.AdminFee, t.Margin, t.SellingPrice, t.ExternalStatus, t.ExternalMessage, t.CreatedAt, t.UpdatedAt)
    return err
}

func (s *Store) UpdatePayment(ctx context.Context, refID string, externalStatus, externalMessage string, rawData map[string]any) error {
    var rawDataJSON string
    if rawData != nil {
        if b, err := json.Marshal(rawData); err == nil {
            rawDataJSON = string(b)
        }
    }
    _, err := s.db.ExecContext(ctx, `
UPDATE transactions SET external_status = ?, external_message = ?, raw_data = ?, updated_at = ? WHERE ref_id = ?
`, externalStatus, externalMessage, rawDataJSON, time.Now(), refID)
    return err
}

func (s *Store) GetByRefID(ctx context.Context, refID string) (*domain.Transaction, error) {
    row := s.db.QueryRowContext(ctx, `
SELECT ref_id, action, product_code, customer_no, bill_amount, admin_fee, margin, selling_price, external_status, external_message, raw_data, created_at, updated_at
FROM transactions WHERE ref_id = ?
`, refID)
    var t domain.Transaction
    var action string
    var rawDataJSON sql.NullString
    if err := row.Scan(&t.RefID, &action, &t.ProductCode, &t.CustomerNo, &t.BillAmount, &t.AdminFee, &t.Margin, &t.SellingPrice, &t.ExternalStatus, &t.ExternalMessage, &rawDataJSON, &t.CreatedAt, &t.UpdatedAt); err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, nil
        }
        return nil, err
    }
    t.Action = domain.TransactionAction(action)
    // Parse raw_data if available
    if rawDataJSON.Valid && rawDataJSON.String != "" {
        if err := json.Unmarshal([]byte(rawDataJSON.String), &t.RawData); err != nil {
            // Log error but don't fail
            fmt.Printf("failed to parse raw_data for ref_id=%s: %v\n", refID, err)
        }
    }
    return &t, nil
}

// PrepaidCache represents a cached prepaid transaction
type PrepaidCache struct {
    CustomerNo string
    ProductCode string
    RawData    map[string]any
    SN         string
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

// GetByCustomerNoAndProductCode retrieves a prepaid cache entry
func (s *Store) GetByCustomerNoAndProductCode(ctx context.Context, customerNo, productCode string) (*PrepaidCache, error) {
    row := s.db.QueryRowContext(ctx, `
SELECT customer_no, product_code, raw_data, sn, created_at, updated_at
FROM prepaid_cache WHERE customer_no = ? AND product_code = ?
`, customerNo, productCode)
    var cache PrepaidCache
    var rawDataJSON string
    if err := row.Scan(&cache.CustomerNo, &cache.ProductCode, &rawDataJSON, &cache.SN, &cache.CreatedAt, &cache.UpdatedAt); err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, nil
        }
        return nil, err
    }
    // Parse raw_data JSON
    if err := json.Unmarshal([]byte(rawDataJSON), &cache.RawData); err != nil {
        return nil, fmt.Errorf("failed to parse raw_data JSON: %w", err)
    }
    return &cache, nil
}

// UpsertPrepaid saves or updates a prepaid cache entry
func (s *Store) UpsertPrepaid(ctx context.Context, customerNo, productCode string, rawData map[string]any, sn string) error {
    rawDataJSON, err := json.Marshal(rawData)
    if err != nil {
        return fmt.Errorf("failed to marshal raw_data: %w", err)
    }
    now := time.Now()
    _, err = s.db.ExecContext(ctx, `
INSERT INTO prepaid_cache (customer_no, product_code, raw_data, sn, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(customer_no, product_code) DO UPDATE SET
    raw_data=excluded.raw_data,
    sn=excluded.sn,
    updated_at=excluded.updated_at
`, customerNo, productCode, string(rawDataJSON), sn, now, now)
    return err
}


