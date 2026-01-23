package main

import (
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "os"
    "strings"
    "time"
)

type digiflazzData struct {
    Status  string  `json:"status"`
    Message string  `json:"message"`
    RC      string  `json:"rc"`
    Price   float64 `json:"price"`
}

type digiflazzResp struct {
    Data digiflazzData `json:"data"`
}

type testCase struct {
    Name        string
    Action      string // inquiry|payment
    SKU         string
    CustomerNo  string
    Expect      string // Sukses|Gagal|Pending
}

func call(baseURL string, action, refID, sku, cust string) (digiflazzResp, error) {
    u, _ := url.Parse(baseURL)
    q := u.Query()
    q.Set("action", action)
    q.Set("ref_id", refID)
    q.Set("product_code", sku)
    q.Set("customer_no", cust)
    u.RawQuery = q.Encode()
    req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
    client := &http.Client{Timeout: 30 * time.Second}
    resp, err := client.Do(req)
    if err != nil {
        return digiflazzResp{}, err
    }
    defer resp.Body.Close()
    b, _ := io.ReadAll(resp.Body)
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return digiflazzResp{}, fmt.Errorf("http %s: %s", resp.Status, string(b))
    }
    var out digiflazzResp
    if err := json.Unmarshal(b, &out); err != nil {
        // ignore; fallback below
    }
    if out.Data.Status == "" {
        // fallback parsing for variant shapes
        var m map[string]any
        if json.Unmarshal(b, &m) == nil {
            if d, ok := m["data"].(map[string]any); ok {
                if s, ok := d["status"].(string); ok { out.Data.Status = s }
                if s, ok := d["message"].(string); ok { out.Data.Message = s }
                if p, ok := asFloat(d["price"]); ok { out.Data.Price = p }
            } else {
                if s, ok := m["status"].(string); ok { out.Data.Status = s }
                if s, ok := m["message"].(string); ok { out.Data.Message = s }
            }
        }
    }
    return out, nil
}

func asFloat(v any) (float64, bool) {
    switch t := v.(type) {
    case float64:
        return t, true
    case int:
        return float64(t), true
    case int64:
        return float64(t), true
    case string:
        var f float64
        if err := json.Unmarshal([]byte(t), &f); err == nil {
            return f, true
        }
        return 0, false
    default:
        return 0, false
    }
}

func main() {
    base := os.Getenv("APP_BASE_URL")
    if base == "" {
        base = "http://localhost:8080/api/otomax"
    }
    // Digiflazz Test Cases (subset dari dokumentasi resmi)
    cases := []testCase{
        // HP / Lainnya
        {Name: "HP Inquiry Sukses", Action: "inquiry", SKU: "hp", CustomerNo: "081234554320", Expect: "Sukses"},
        {Name: "HP Inquiry Gagal", Action: "inquiry", SKU: "hp", CustomerNo: "081234554321", Expect: "Gagal"},
        {Name: "HP Payment Sukses", Action: "payment", SKU: "hp", CustomerNo: "081234554320", Expect: "Sukses"},
        {Name: "HP Payment Gagal", Action: "payment", SKU: "hp", CustomerNo: "081234554324", Expect: "Gagal"},
        {Name: "HP Payment Pending", Action: "payment", SKU: "hp", CustomerNo: "081244554320", Expect: "Pending"},

        // PLN
        {Name: "PLN Inquiry Sukses (1)", Action: "inquiry", SKU: "pln", CustomerNo: "530000000001", Expect: "Sukses"},
        {Name: "PLN Inquiry Gagal", Action: "inquiry", SKU: "pln", CustomerNo: "530000000003", Expect: "Gagal"},
        {Name: "PLN Payment Sukses (1)", Action: "payment", SKU: "pln", CustomerNo: "530000000001", Expect: "Sukses"},
        {Name: "PLN Payment Pending", Action: "payment", SKU: "pln", CustomerNo: "630000000001", Expect: "Pending"},
        {Name: "PLN Payment Gagal", Action: "payment", SKU: "pln", CustomerNo: "530000000006", Expect: "Gagal"},

        // PDAM
        {Name: "PDAM Inquiry Sukses", Action: "inquiry", SKU: "pdam", CustomerNo: "1013226", Expect: "Sukses"},
        {Name: "PDAM Inquiry Gagal", Action: "inquiry", SKU: "pdam", CustomerNo: "1013227", Expect: "Gagal"},
        {Name: "PDAM Payment Sukses", Action: "payment", SKU: "pdam", CustomerNo: "1013226", Expect: "Sukses"},
        {Name: "PDAM Payment Pending", Action: "payment", SKU: "pdam", CustomerNo: "2013226", Expect: "Pending"},
        {Name: "PDAM Payment Gagal", Action: "payment", SKU: "pdam", CustomerNo: "1013230", Expect: "Gagal"},

        // INTERNET
        {Name: "INTERNET Inquiry Sukses", Action: "inquiry", SKU: "internet", CustomerNo: "6391601001", Expect: "Sukses"},
        {Name: "INTERNET Inquiry Gagal", Action: "inquiry", SKU: "internet", CustomerNo: "6391601002", Expect: "Gagal"},
        {Name: "INTERNET Payment Sukses", Action: "payment", SKU: "internet", CustomerNo: "6391601001", Expect: "Sukses"},
        {Name: "INTERNET Payment Pending", Action: "payment", SKU: "internet", CustomerNo: "7391601001", Expect: "Pending"},
        {Name: "INTERNET Payment Gagal", Action: "payment", SKU: "internet", CustomerNo: "6391601005", Expect: "Gagal"},

        // BPJS KESEHATAN
        {Name: "BPJS Inquiry Sukses", Action: "inquiry", SKU: "bpjs", CustomerNo: "8801234560001", Expect: "Sukses"},
        {Name: "BPJS Inquiry Gagal", Action: "inquiry", SKU: "bpjs", CustomerNo: "8801234560002", Expect: "Gagal"},
        {Name: "BPJS Payment Sukses", Action: "payment", SKU: "bpjs", CustomerNo: "8801234560001", Expect: "Sukses"},
        {Name: "BPJS Payment Pending", Action: "payment", SKU: "bpjs", CustomerNo: "9801234560001", Expect: "Pending"},
        {Name: "BPJS Payment Gagal", Action: "payment", SKU: "bpjs", CustomerNo: "8801234560005", Expect: "Gagal"},

        // Multifinance
        {Name: "Multifinance Inquiry Sukses", Action: "inquiry", SKU: "multifinance", CustomerNo: "6391601201", Expect: "Sukses"},
        {Name: "Multifinance Inquiry Gagal", Action: "inquiry", SKU: "multifinance", CustomerNo: "6391601202", Expect: "Gagal"},
        {Name: "Multifinance Payment Sukses", Action: "payment", SKU: "multifinance", CustomerNo: "6391601201", Expect: "Sukses"},
        {Name: "Multifinance Payment Pending", Action: "payment", SKU: "multifinance", CustomerNo: "7391601201", Expect: "Pending"},
        {Name: "Multifinance Payment Gagal", Action: "payment", SKU: "multifinance", CustomerNo: "6391601205", Expect: "Gagal"},

        // PBB (contoh: cimahi)
        {Name: "PBB Inquiry Sukses", Action: "inquiry", SKU: "cimahi", CustomerNo: "329801092375999991", Expect: "Sukses"},
        {Name: "PBB Inquiry Gagal", Action: "inquiry", SKU: "cimahi", CustomerNo: "329801092375999992", Expect: "Gagal"},
        {Name: "PBB Payment Sukses", Action: "payment", SKU: "cimahi", CustomerNo: "329801092375999991", Expect: "Sukses"},
        {Name: "PBB Payment Pending", Action: "payment", SKU: "cimahi", CustomerNo: "429801092375999991", Expect: "Pending"},
        {Name: "PBB Payment Gagal", Action: "payment", SKU: "cimahi", CustomerNo: "329801092375999995", Expect: "Gagal"},

        // Pajak Daerah Lainnya (pdl)
        {Name: "PDL Inquiry Sukses", Action: "inquiry", SKU: "pdl", CustomerNo: "3298010921", Expect: "Sukses"},
        {Name: "PDL Inquiry Gagal", Action: "inquiry", SKU: "pdl", CustomerNo: "3298010922", Expect: "Gagal"},
        {Name: "PDL Payment Sukses", Action: "payment", SKU: "pdl", CustomerNo: "3298010921", Expect: "Sukses"},
        {Name: "PDL Payment Pending", Action: "payment", SKU: "pdl", CustomerNo: "4298010921", Expect: "Pending"},
        {Name: "PDL Payment Gagal", Action: "payment", SKU: "pdl", CustomerNo: "3298010923", Expect: "Gagal"},

        // Gas Negara (pgas)
        {Name: "PGAS Inquiry Sukses", Action: "inquiry", SKU: "pgas", CustomerNo: "0110014601", Expect: "Sukses"},
        {Name: "PGAS Inquiry Gagal", Action: "inquiry", SKU: "pgas", CustomerNo: "0110014602", Expect: "Gagal"},
        {Name: "PGAS Payment Sukses", Action: "payment", SKU: "pgas", CustomerNo: "0110014601", Expect: "Sukses"},
        {Name: "PGAS Payment Pending", Action: "payment", SKU: "pgas", CustomerNo: "1110014601", Expect: "Pending"},
        {Name: "PGAS Payment Gagal", Action: "payment", SKU: "pgas", CustomerNo: "0110014605", Expect: "Gagal"},

        // TV
        {Name: "TV Inquiry Sukses", Action: "inquiry", SKU: "tv", CustomerNo: "127246500101", Expect: "Sukses"},
        {Name: "TV Inquiry Gagal", Action: "inquiry", SKU: "tv", CustomerNo: "127246500102", Expect: "Gagal"},
        {Name: "TV Payment Sukses", Action: "payment", SKU: "tv", CustomerNo: "127246500101", Expect: "Sukses"},
        {Name: "TV Payment Pending", Action: "payment", SKU: "tv", CustomerNo: "227246500101", Expect: "Pending"},
        {Name: "TV Payment Gagal", Action: "payment", SKU: "tv", CustomerNo: "127246500105", Expect: "Gagal"},

        // BPJSTK
        {Name: "BPJSTK Inquiry Sukses", Action: "inquiry", SKU: "bpjstk", CustomerNo: "8102051011270001", Expect: "Sukses"},
        {Name: "BPJSTK Inquiry Gagal", Action: "inquiry", SKU: "bpjstk", CustomerNo: "8102051011270002", Expect: "Gagal"},
        {Name: "BPJSTK Payment Sukses", Action: "payment", SKU: "bpjstk", CustomerNo: "8102051011270001", Expect: "Sukses"},
        {Name: "BPJSTK Payment Pending", Action: "payment", SKU: "bpjstk", CustomerNo: "9102051011270001", Expect: "Pending"},
        {Name: "BPJSTK Payment Gagal", Action: "payment", SKU: "bpjstk", CustomerNo: "8102051011270003", Expect: "Gagal"},

        // BPJSTKPU
        {Name: "BPJSTKPU Inquiry Sukses", Action: "inquiry", SKU: "bpjstkpu", CustomerNo: "400000100001", Expect: "Sukses"},
        {Name: "BPJSTKPU Inquiry Gagal", Action: "inquiry", SKU: "bpjstkpu", CustomerNo: "400000100002", Expect: "Gagal"},
        {Name: "BPJSTKPU Payment Sukses", Action: "payment", SKU: "bpjstkpu", CustomerNo: "400000100001", Expect: "Sukses"},
        {Name: "BPJSTKPU Payment Pending", Action: "payment", SKU: "bpjstkpu", CustomerNo: "500000100001", Expect: "Pending"},
        {Name: "BPJSTKPU Payment Gagal", Action: "payment", SKU: "bpjstkpu", CustomerNo: "400000100003", Expect: "Gagal"},

        // PLN Nontaglis
        {Name: "PLN Nontaglis Inquiry Sukses", Action: "inquiry", SKU: "plnnontaglist", CustomerNo: "3225030005921", Expect: "Sukses"},
        {Name: "PLN Nontaglis Inquiry Gagal", Action: "inquiry", SKU: "plnnontaglist", CustomerNo: "3225030005922", Expect: "Gagal"},
        {Name: "PLN Nontaglis Payment Sukses", Action: "payment", SKU: "plnnontaglist", CustomerNo: "3225030005921", Expect: "Sukses"},
        {Name: "PLN Nontaglis Payment Pending", Action: "payment", SKU: "plnnontaglist", CustomerNo: "4225030005921", Expect: "Pending"},
        {Name: "PLN Nontaglis Payment Gagal", Action: "payment", SKU: "plnnontaglist", CustomerNo: "3225030005923", Expect: "Gagal"},

        // E-Money
        {Name: "E-Money Inquiry Sukses", Action: "inquiry", SKU: "emoney", CustomerNo: "082100000001", Expect: "Sukses"},
        {Name: "E-Money Inquiry Gagal", Action: "inquiry", SKU: "emoney", CustomerNo: "082100000002", Expect: "Gagal"},
        {Name: "E-Money Payment Sukses", Action: "payment", SKU: "emoney", CustomerNo: "082100000001", Expect: "Sukses"},
        {Name: "E-Money Payment Pending", Action: "payment", SKU: "emoney", CustomerNo: "082110000001", Expect: "Pending"},
        {Name: "E-Money Payment Gagal", Action: "payment", SKU: "emoney", CustomerNo: "082100000003", Expect: "Gagal"},

        // SAMSAT
        {Name: "SAMSAT Inquiry Sukses", Action: "inquiry", SKU: "samsat", CustomerNo: "9658548523568701,0212502110170100", Expect: "Sukses"},
        {Name: "SAMSAT Inquiry Gagal", Action: "inquiry", SKU: "samsat", CustomerNo: "9658548523568702,0212502110170100", Expect: "Gagal"},
        {Name: "SAMSAT Payment Sukses", Action: "payment", SKU: "samsat", CustomerNo: "9658548523568701,0212502110170100", Expect: "Sukses"},
        {Name: "SAMSAT Payment Pending", Action: "payment", SKU: "samsat", CustomerNo: "0658548523568701,0212502110170100", Expect: "Pending"},
        {Name: "SAMSAT Payment Gagal", Action: "payment", SKU: "samsat", CustomerNo: "9658548523568705,0212502110170100", Expect: "Gagal"},
    }

    failures := 0
    for i, tc := range cases {
        refID := fmt.Sprintf("test-%d-%d", time.Now().Unix(), i)
        // For payment, ensure inquiry is done first with the SAME ref_id
        if strings.ToLower(tc.Action) == "payment" {
            if _, err := call(base, "inquiry", refID, tc.SKU, tc.CustomerNo); err != nil {
                fmt.Printf("[WARN] pre-inquiry failed for %s: %v\n", tc.Name, err)
            }
        }
        res, err := call(base, tc.Action, refID, tc.SKU, tc.CustomerNo)
        if err != nil {
            fmt.Printf("[FAIL] %s error=%v\n", tc.Name, err)
            failures++
            continue
        }
        got := strings.ToLower(res.Data.Status)
        want := strings.ToLower(tc.Expect)
        if got != want {
            fmt.Printf("[FAIL] %s expect=%s got=%s message=%s\n", tc.Name, tc.Expect, res.Data.Status, res.Data.Message)
            failures++
        } else {
            fmt.Printf("[PASS] %s status=%s price=%.0f message=%s\n", tc.Name, res.Data.Status, res.Data.Price, res.Data.Message)
        }
    }
    if failures > 0 {
        os.Exit(1)
    }
}


