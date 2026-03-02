package otomax

import (
    "bytes"
    "context"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/base64"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "strings"
    "time"

    "digiflazz-api/internal/config"
)

type Client struct {
    httpClient *http.Client
    cfg        *config.Config
}

func New(cfg *config.Config) *Client {
    return &Client{
        cfg: cfg,
        httpClient: &http.Client{Timeout: time.Duration(cfg.RequestTimeoutMs) * time.Millisecond},
    }
}

// cleanBase64String converts Base64 to Base64Url encoding
// Replaces + with -, / with _, and removes padding =
func cleanBase64String(s string) string {
    s = strings.ReplaceAll(s, "+", "-")
    s = strings.ReplaceAll(s, "/", "_")
    s = strings.ReplaceAll(s, "=", "")
    return s
}

// generateToken generates OtomaX API token based on the provided payload
// Algorithm (matching working implementation):
// 1. Metadata = {"id": AppID} -> Base64 -> Base64Url (clean)
// 2. FirstSignature = HMACSHA256(DevKey, AppKey) -> Base64 (not cleaned)
// 3. SecondSignature = HMACSHA256(FirstSignature, payload) -> Base64 -> Base64Url (clean)
// 4. Token = {Metadata}.{SecondSignature}
func (c *Client) generateToken(payload []byte) (string, error) {
    // Step 1: Create metadata
    metadata := map[string]string{
        "id": c.cfg.OtomaxAppID,
    }
    
    metadataJSON, err := json.Marshal(metadata)
    if err != nil {
        return "", fmt.Errorf("failed to marshal metadata: %w", err)
    }
    
    // Step 2: Encode metadata to Base64 and clean it
    metadataEncoded := base64.StdEncoding.EncodeToString(metadataJSON)
    metadataEncoded = cleanBase64String(metadataEncoded)
    
    // Step 3: Generate first signature (HMAC-SHA256(devKey, appKey))
    // Note: devKey is the HMAC key, appKey is the message
    h := hmac.New(sha256.New, []byte(c.cfg.OtomaxDevKey))
    h.Write([]byte(c.cfg.OtomaxAppKey))
    firstSignature := base64.StdEncoding.EncodeToString(h.Sum(nil))
    
    // Step 4: Generate second signature (HMAC-SHA256(firstSignature, requestBody))
    // Note: firstSignature is the HMAC key, payload is the message
    h = hmac.New(sha256.New, []byte(firstSignature))
    h.Write(payload)
    secondSignature := base64.StdEncoding.EncodeToString(h.Sum(nil))
    secondSignature = cleanBase64String(secondSignature)
    
    // Step 5: Combine metadata and signature
    token := metadataEncoded + "." + secondSignature
    
    // Debug: log token generation details (only in development)
    if c.cfg.IsDevelopment() {
        fmt.Printf("[DEBUG] Token generation:\n")
        fmt.Printf("  AppID=%s, AppKey=%s, DevKey=%s\n", c.cfg.OtomaxAppID, c.cfg.OtomaxAppKey, c.cfg.OtomaxDevKey)
        fmt.Printf("  Payload=%s\n", string(payload))
        fmt.Printf("  Metadata=%s\n", metadataEncoded)
        fmt.Printf("  FirstSignature=%s\n", firstSignature)
        fmt.Printf("  SecondSignature=%s\n", secondSignature)
        fmt.Printf("  Token=%s\n", token)
    }
    
    return token, nil
}

// GetTrxRequest represents request for GetTrx
type GetTrxRequest struct {
    TrxID interface{} `json:"trxid"` // Can be string or number
}

// GetTrxResponse represents response from GetTrx
type GetTrxResponse struct {
    OK     bool `json:"ok"`
    Result struct {
        Kode          interface{} `json:"kode"` // Can be string or number
        KodeProduk    string       `json:"kode_produk"`
        Tujuan        string       `json:"tujuan"`
        KodeReseller  string       `json:"kode_reseller"`
        Harga         float64      `json:"harga"`
        HargaBeli     float64      `json:"harga_beli"`
        Status        int          `json:"status"`
    } `json:"result"`
}

// buildURL constructs the full URL for an endpoint
func (c *Client) buildURL(endpoint string) (string, error) {
    if c.cfg.OtomaxAPIBaseURL == "" {
        return "", fmt.Errorf("OTOMAX_API_BASE_URL is not configured")
    }
    baseURL := strings.TrimSuffix(c.cfg.OtomaxAPIBaseURL, "/")
    if !strings.HasPrefix(endpoint, "/") {
        endpoint = "/" + endpoint
    }
    return baseURL + endpoint, nil
}

// GetTrx gets transaction details by ref_id (trxid)
func (c *Client) GetTrx(ctx context.Context, refID string) (*GetTrxResponse, error) {
    payload := GetTrxRequest{TrxID: refID}
    // Marshal without indentation to match Postman's pm.request.body.raw format
    body, err := json.Marshal(payload)
    if err != nil {
        return nil, fmt.Errorf("otomax GetTrx marshal error: %w", err)
    }
    
    // Generate token based on payload (payload is raw JSON bytes, matching Postman)
    token, err := c.generateToken(body)
    if err != nil {
        return nil, fmt.Errorf("otomax generate token error: %w", err)
    }
    
    url, err := c.buildURL("GetTrx")
    if err != nil {
        return nil, fmt.Errorf("otomax URL error: %w", err)
    }
    req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Token", token)
    
    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("otomax GetTrx request error: %w", err)
    }
    defer resp.Body.Close()
    
    bodyBytes, _ := io.ReadAll(resp.Body)
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return nil, fmt.Errorf("otomax GetTrx http error %d: %s", resp.StatusCode, string(bodyBytes))
    }
    
    // Debug: log raw response (only in development)
    if c.cfg.IsDevelopment() {
        fmt.Printf("[DEBUG] OtomaX GetTrx request: ref_id=%s\n", refID)
        fmt.Printf("[DEBUG] OtomaX GetTrx response (status=%d): %s\n", resp.StatusCode, string(bodyBytes))
    }
    
    var result GetTrxResponse
    if err := json.Unmarshal(bodyBytes, &result); err != nil {
        if c.cfg.IsDevelopment() {
            fmt.Printf("[DEBUG] OtomaX GetTrx JSON parse error: %v\n", err)
        }
        return nil, fmt.Errorf("otomax GetTrx decode error: %w body=%s", err, string(bodyBytes))
    }
    
    // Check if response is OK and has result
    if !result.OK || result.Result.KodeProduk == "" {
        if c.cfg.IsDevelopment() {
            fmt.Printf("[DEBUG] OtomaX GetTrx: ok=%v, result empty or invalid\n", result.OK)
        }
        return nil, nil
    }
    
    // Debug: log parsed result (only in development)
    if c.cfg.IsDevelopment() {
        fmt.Printf("[DEBUG] OtomaX GetTrx parsed: Kode=%v, KodeProduk=%s, Tujuan=%s, KodeReseller=%s, Harga=%.0f, Status=%d\n", 
            result.Result.Kode, result.Result.KodeProduk, result.Result.Tujuan, result.Result.KodeReseller, result.Result.Harga, result.Result.Status)
    }
    
    return &result, nil
}

// GetRsRequest represents request for GetRs
type GetRsRequest struct {
    Kode string `json:"kode"`
}

// GetRsResponse represents response from GetRs
type GetRsResponse struct {
    Kode        string `json:"kode"`
    Nama        string `json:"nama"`
    Saldo       int64  `json:"saldo"`
    SaldoMinimal int64 `json:"saldo_minimal"`
}

// GetRs gets reseller details by kode
func (c *Client) GetRs(ctx context.Context, kode string) (*GetRsResponse, error) {
    payload := GetRsRequest{Kode: kode}
    body, _ := json.Marshal(payload)
    
    // Generate token based on payload
    token, err := c.generateToken(body)
    if err != nil {
        return nil, fmt.Errorf("otomax generate token error: %w", err)
    }
    
    url, err := c.buildURL("GetRs")
    if err != nil {
        return nil, fmt.Errorf("otomax URL error: %w", err)
    }
    req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Token", token)
    
    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("otomax GetRs request error: %w", err)
    }
    defer resp.Body.Close()
    
    bodyBytes, _ := io.ReadAll(resp.Body)
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return nil, fmt.Errorf("otomax GetRs http error %d: %s", resp.StatusCode, string(bodyBytes))
    }
    
    var result GetRsResponse
    if err := json.Unmarshal(bodyBytes, &result); err != nil {
        return nil, fmt.Errorf("otomax GetRs decode error: %w body=%s", err, string(bodyBytes))
    }
    
    return &result, nil
}

// GetSaldoRsRequest represents request for GetSaldoRs
type GetSaldoRsRequest struct {
    Kode string `json:"kode"`
}

// GetSaldoRsResponse represents response from GetSaldoRs
type GetSaldoRsResponse struct {
    OK     bool `json:"ok"`
    Result struct {
        Saldo        float64 `json:"saldo"`
        SaldoMinimal float64 `json:"saldo_minimal"`
    } `json:"result"`
}

// GetSaldoRs gets reseller balance and status by kode
func (c *Client) GetSaldoRs(ctx context.Context, kode string) (*GetSaldoRsResponse, error) {
    payload := GetSaldoRsRequest{Kode: kode}
    body, _ := json.Marshal(payload)
    
    // Generate token based on payload
    token, err := c.generateToken(body)
    if err != nil {
        return nil, fmt.Errorf("otomax generate token error: %w", err)
    }
    
    url, err := c.buildURL("GetSaldoRs")
    if err != nil {
        return nil, fmt.Errorf("otomax URL error: %w", err)
    }
    req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Token", token)
    
    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("otomax GetSaldoRs request error: %w", err)
    }
    defer resp.Body.Close()
    
    bodyBytes, _ := io.ReadAll(resp.Body)
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return nil, fmt.Errorf("otomax GetSaldoRs http error %d: %s", resp.StatusCode, string(bodyBytes))
    }
    
    // Debug: log raw response (only in development)
    if c.cfg.IsDevelopment() {
        fmt.Printf("[DEBUG] OtomaX GetSaldoRs request: kode=%s\n", kode)
        fmt.Printf("[DEBUG] OtomaX GetSaldoRs response (status=%d): %s\n", resp.StatusCode, string(bodyBytes))
    }
    
    var result GetSaldoRsResponse
    if err := json.Unmarshal(bodyBytes, &result); err != nil {
        if c.cfg.IsDevelopment() {
            fmt.Printf("[DEBUG] OtomaX GetSaldoRs JSON parse error: %v\n", err)
        }
        return nil, fmt.Errorf("otomax GetSaldoRs decode error: %w body=%s", err, string(bodyBytes))
    }
    
    // Check if response is OK and has result
    if !result.OK {
        if c.cfg.IsDevelopment() {
            fmt.Printf("[DEBUG] OtomaX GetSaldoRs: ok=false\n")
        }
        return nil, nil
    }
    
    // Debug: log parsed result (only in development)
    if c.cfg.IsDevelopment() {
        fmt.Printf("[DEBUG] OtomaX GetSaldoRs parsed: Saldo=%.0f, SaldoMinimal=%.0f\n", 
            result.Result.Saldo, result.Result.SaldoMinimal)
    }
    
    return &result, nil
}

// TambahSaldoRequest represents request for TambahSaldo
type TambahSaldoRequest struct {
    KodeReseller string `json:"kode_reseller"`
    Jumlah       int64  `json:"jumlah"`  // Negative untuk tarik saldo
    Saldo        int64  `json:"saldo,omitempty"`   // Saldo setelah perubahan (optional)
    Keterangan   string `json:"keterangan"`
}

// TambahSaldoResponse represents response from TambahSaldo
type TambahSaldoResponse struct {
    OK   bool   `json:"ok"`
    Desc string `json:"desc"`
    Result struct {
        Saldo float64 `json:"saldo,omitempty"`
    } `json:"result,omitempty"`
}

// TambahSaldo adds or deducts reseller balance (jumlah negative = tarik saldo)
func (c *Client) TambahSaldo(ctx context.Context, req TambahSaldoRequest) (*TambahSaldoResponse, error) {
    body, _ := json.Marshal(req)
    
    // Generate token based on payload
    token, err := c.generateToken(body)
    if err != nil {
        return nil, fmt.Errorf("otomax generate token error: %w", err)
    }
    
    url, err := c.buildURL("TambahSaldo")
    if err != nil {
        return nil, fmt.Errorf("otomax URL error: %w", err)
    }
    httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Token", token)
    
    resp, err := c.httpClient.Do(httpReq)
    if err != nil {
        return nil, fmt.Errorf("otomax TambahSaldo request error: %w", err)
    }
    defer resp.Body.Close()
    
    bodyBytes, _ := io.ReadAll(resp.Body)
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return nil, fmt.Errorf("otomax TambahSaldo http error %d: %s", resp.StatusCode, string(bodyBytes))
    }
    
    // Debug: log raw response (only in development)
    if c.cfg.IsDevelopment() {
        fmt.Printf("[DEBUG] OtomaX TambahSaldo request: kode_reseller=%s jumlah=%d saldo=%d keterangan=%s\n", 
            req.KodeReseller, req.Jumlah, req.Saldo, req.Keterangan)
        fmt.Printf("[DEBUG] OtomaX TambahSaldo response (status=%d): %s\n", resp.StatusCode, string(bodyBytes))
    }
    
    var result TambahSaldoResponse
    if err := json.Unmarshal(bodyBytes, &result); err != nil {
        if c.cfg.IsDevelopment() {
            fmt.Printf("[DEBUG] OtomaX TambahSaldo JSON parse error: %v\n", err)
        }
        return nil, fmt.Errorf("otomax TambahSaldo decode error: %w body=%s", err, string(bodyBytes))
    }
    
    // Debug: log parsed result (only in development)
    if c.cfg.IsDevelopment() {
        if result.OK {
            fmt.Printf("[DEBUG] OtomaX TambahSaldo parsed: OK=true, Desc=%s, Saldo=%.0f\n", 
                result.Desc, result.Result.Saldo)
        } else {
            fmt.Printf("[DEBUG] OtomaX TambahSaldo parsed: OK=false, Desc=%s\n", result.Desc)
        }
    }
    
	if !result.OK {
        return nil, fmt.Errorf("otomax TambahSaldo failed: %s", result.Desc)
    }
    
    return &result, nil
}

// InsertInboxRequest represents request for InsertInbox (forward ke OtomaX)
// DigiFlazz topup: map ref_id, hp, pulsa_code, price ke pesan; pengirim = hp; kode_reseller dari config/GetTrx
type InsertInboxRequest struct {
    Pesan        string `json:"pesan"`         // Format pesan (contoh: "tiket.100000.1234" atau "pulsa_code.hp.price.ref_id")
    KodeReseller string `json:"kode_reseller"`
    Pengirim     string `json:"pengirim"`      // Nomor pengirim (biasanya hp/nomor pelanggan)
    TipePengirim string `json:"tipe_pengirim"` // "W" atau sesuai kontrak OtomaX
}

// InsertInboxResponse represents response from InsertInbox
type InsertInboxResponse struct {
    OK   bool   `json:"ok"`
    Desc string `json:"desc"`
}

// InsertInbox forwards a message to OtomaX inbox (untuk forward request DigiFlazz ke OtomaX)
func (c *Client) InsertInbox(ctx context.Context, req InsertInboxRequest) (*InsertInboxResponse, error) {
    body, err := json.Marshal(req)
    if err != nil {
        return nil, fmt.Errorf("otomax InsertInbox marshal error: %w", err)
    }
    token, err := c.generateToken(body)
    if err != nil {
        return nil, fmt.Errorf("otomax generate token error: %w", err)
    }
    url, err := c.buildURL("InsertInbox")
    if err != nil {
        return nil, fmt.Errorf("otomax URL error: %w", err)
    }
    httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
    if err != nil {
        return nil, fmt.Errorf("otomax InsertInbox request error: %w", err)
    }
    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Token", token)
    resp, err := c.httpClient.Do(httpReq)
    if err != nil {
        return nil, fmt.Errorf("otomax InsertInbox request error: %w", err)
    }
    defer resp.Body.Close()
    bodyBytes, _ := io.ReadAll(resp.Body)
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return nil, fmt.Errorf("otomax InsertInbox http error %d: %s", resp.StatusCode, string(bodyBytes))
    }
    var result InsertInboxResponse
    if err := json.Unmarshal(bodyBytes, &result); err != nil {
        return nil, fmt.Errorf("otomax InsertInbox decode error: %w body=%s", err, string(bodyBytes))
    }
    return &result, nil
}

// QueryProdukRequest represents request for QueryProduk (GetProduct)
type QueryProdukRequest struct {
    Kode string `json:"kode"` // Kode produk (pulsa_code / buyer_sku_code)
}

// QueryProdukResponse represents response from QueryProduk (harga_jual untuk pembandingan price)
type QueryProdukResponse struct {
    OK     bool `json:"ok"`
    Result struct {
        Kode      string  `json:"kode,omitempty"`
        HargaJual float64 `json:"harga_jual,omitempty"`
        HargaBeli float64 `json:"harga_beli,omitempty"`
        Nama      string  `json:"nama,omitempty"`
        // Field lain dari OtomaX bisa ditambah sesuai response aktual
    } `json:"result"`
}

// QueryProduk gets product by kode (untuk pembandingan: price DigiFlazz tidak boleh lebih murah dari harga_jual)
func (c *Client) QueryProduk(ctx context.Context, kode string) (*QueryProdukResponse, error) {
    payload := QueryProdukRequest{Kode: kode}
    body, err := json.Marshal(payload)
    if err != nil {
        return nil, fmt.Errorf("otomax QueryProduk marshal error: %w", err)
    }
    token, err := c.generateToken(body)
    if err != nil {
        return nil, fmt.Errorf("otomax generate token error: %w", err)
    }
    url, err := c.buildURL("QueryProduk")
    if err != nil {
        return nil, fmt.Errorf("otomax URL error: %w", err)
    }
    httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
    if err != nil {
        return nil, fmt.Errorf("otomax QueryProduk request error: %w", err)
    }
    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Token", token)
    resp, err := c.httpClient.Do(httpReq)
    if err != nil {
        return nil, fmt.Errorf("otomax QueryProduk request error: %w", err)
    }
    defer resp.Body.Close()
    bodyBytes, _ := io.ReadAll(resp.Body)
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return nil, fmt.Errorf("otomax QueryProduk http error %d: %s", resp.StatusCode, string(bodyBytes))
    }
    var result QueryProdukResponse
    if err := json.Unmarshal(bodyBytes, &result); err != nil {
        return nil, fmt.Errorf("otomax QueryProduk decode error: %w body=%s", err, string(bodyBytes))
    }
    return &result, nil
}

