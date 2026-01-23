package digiflazz

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
		cfg:        cfg,
		httpClient: &http.Client{Timeout: time.Duration(cfg.RequestTimeoutMs) * time.Millisecond},
	}
}

type InquiryRequest struct {
	Commands     string `json:"commands"`
	Username     string `json:"username"`
	BuyerSKUCode string `json:"buyer_sku_code"`
	CustomerNo   string `json:"customer_no"`
	RefID        string `json:"ref_id"`
	Sign         string `json:"sign"`
	Testing      bool   `json:"testing,omitempty"`
}

type PaymentRequest struct {
	Commands     string `json:"commands"`
	Username     string `json:"username"`
	BuyerSKUCode string `json:"buyer_sku_code"`
	CustomerNo   string `json:"customer_no"`
	RefID        string `json:"ref_id"`
	Sign         string `json:"sign"`
	Testing      bool   `json:"testing,omitempty"`
}

type InquiryResult struct {
	Status     string
	Message    string
	BillAmount int64
	Raw        map[string]any
}

type PaymentResult struct {
	Status     string
	Message    string
	PaidAmount int64
	Raw        map[string]any
}

type PLNInquiryRequest struct {
	Username   string `json:"username"`
	CustomerNo string `json:"customer_no"`
	Sign       string `json:"sign"`
}

type PLNInquiryResult struct {
	Status       string
	Message      string
	RC           string
	CustomerNo   string
	MeterNo      string
	SubscriberID string
	Name         string
	SegmentPower string
	Raw          map[string]any
}

type StatusRequest struct {
	Commands     string `json:"commands"`
	Username     string `json:"username"`
	BuyerSKUCode string `json:"buyer_sku_code"`
	CustomerNo   string `json:"customer_no"`
	RefID        string `json:"ref_id"`
	Sign         string `json:"sign"`
	Testing      bool   `json:"testing,omitempty"`
}

type StatusResult struct {
	Status  string
	Message string
	RC      string
	Raw     map[string]any
}

// Prepaid Topup
type TopupRequest struct {
	Username     string `json:"username"`
	BuyerSKUCode string `json:"buyer_sku_code"`
	CustomerNo   string `json:"customer_no"`
	RefID        string `json:"ref_id"`
	Sign         string `json:"sign"`
	Testing      bool   `json:"testing,omitempty"`
	CallbackURL  string `json:"cb_url,omitempty"`
	MaxPrice     int64  `json:"max_price,omitempty"`
}

type TopupResult struct {
	Status  string
	Message string
	Price   int64
	SN      string
	Raw     map[string]any
}

func (c *Client) sign(refID string) string {
	// Verify against official docs: typically md5(username+api_key+ref_id)
	h := md5.Sum([]byte(c.cfg.DigiflazzUsername + c.cfg.DigiflazzAPIKey + refID))
	return hex.EncodeToString(h[:])
}

func (c *Client) signCustomer(customerNo string) string {
	h := md5.Sum([]byte(c.cfg.DigiflazzUsername + c.cfg.DigiflazzAPIKey + customerNo))
	return hex.EncodeToString(h[:])
}

func (c *Client) Inquiry(ctx context.Context, productCode, customerNo, refID string) (*InquiryResult, error) {
	// Return mockup data if mockup mode is enabled
	if c.cfg.MockupMode {
		if c.cfg.IsDevelopment() || os.Getenv("DEBUG_CONFIG") == "true" {
			fmt.Printf("[DEBUG] Using mockup inquiry data (MockupMode=true) for ref_id=%s\n", refID)
		}
		return getMockupInquiry(ctx, productCode, customerNo, refID)
	}

	payload := InquiryRequest{
		Commands:     "inq-pasca",
		Username:     c.cfg.DigiflazzUsername,
		BuyerSKUCode: productCode,
		CustomerNo:   customerNo,
		RefID:        refID,
		Sign:         c.sign(refID),
		Testing:      strings.ToLower(c.cfg.Env) != "production",
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s%s", c.cfg.DigiflazzBaseURL, "/v1/transaction")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		return nil, fmt.Errorf("digiflazz inquiry decode error: %v body=%s", err, string(bodyBytes))
	}
	res := &InquiryResult{Raw: raw}
	if data, ok := raw["data"].(map[string]any); ok {
		if v, ok := data["status"].(string); ok {
			res.Status = v
		}
		if v, ok := data["message"].(string); ok {
			res.Message = v
		}
		// Use 'price' as bill amount per docs
		if b, ok := asInt64(data["price"]); ok {
			res.BillAmount = b
		}
		// Fallbacks
		if res.BillAmount == 0 {
			if b, ok := asInt64(data["bill"]); ok {
				res.BillAmount = b
			}
		}
	}
	return res, nil
}

func (c *Client) Payment(ctx context.Context, productCode, customerNo, refID string) (*PaymentResult, error) {
	// Return mockup data if mockup mode is enabled
	if c.cfg.MockupMode {
		return getMockupPayment(ctx, productCode, customerNo, refID)
	}

	payload := PaymentRequest{
		Commands:     "pay-pasca",
		Username:     c.cfg.DigiflazzUsername,
		BuyerSKUCode: productCode,
		CustomerNo:   customerNo,
		RefID:        refID,
		Sign:         c.sign(refID),
		Testing:      strings.ToLower(c.cfg.Env) != "production",
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s%s", c.cfg.DigiflazzBaseURL, "/v1/transaction")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		return nil, fmt.Errorf("digiflazz payment decode error: %v body=%s", err, string(bodyBytes))
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
		if res.PaidAmount == 0 {
			if b, ok := asInt64(data["paid"]); ok {
				res.PaidAmount = b
			}
		}
	}
	return res, nil
}

func asInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case int64:
		return t, true
	case int:
		return int64(t), true
	case string:
		var x int64
		if err := json.Unmarshal([]byte(t), &x); err == nil {
			return x, true
		}
		return 0, false
	default:
		return 0, false
	}
}

// Price List Pasca
type priceListRequest struct {
	Cmd      string `json:"cmd"`
	Username string `json:"username"`
	Sign     string `json:"sign"`
}

type PriceListPascaItem struct {
	BuyerSKUCode       string `json:"buyer_sku_code"`
	BuyerProductStatus bool   `json:"buyer_product_status"`
	Brand              string `json:"brand"`
	ProductName        string `json:"product_name"`
}

type priceListPascaResponse struct {
	Data []PriceListPascaItem `json:"data"`
}

func (c *Client) pricelistSign() string {
	// md5(username + apiKey + "pricelist") per docs
	h := md5.Sum([]byte(c.cfg.DigiflazzUsername + c.cfg.DigiflazzAPIKey + "pricelist"))
	return hex.EncodeToString(h[:])
}

func (c *Client) GetPriceListPasca(ctx context.Context) ([]PriceListPascaItem, error) {
	payload := priceListRequest{
		Cmd:      "pasca",
		Username: c.cfg.DigiflazzUsername,
		Sign:     c.pricelistSign(),
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s%s", c.cfg.DigiflazzBaseURL, "/v1/price-list")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out priceListPascaResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("digiflazz pricelist pasca decode error: %v body=%s", err, string(b))
	}
	return out.Data, nil
}

// Prepaid price list
type PriceListPrepaidItem struct {
	BuyerSKUCode       string  `json:"buyer_sku_code"`
	BuyerProductStatus bool    `json:"buyer_product_status"`
	Brand              string  `json:"brand"`
	ProductName        string  `json:"product_name"`
	Price              float64 `json:"price,omitempty"`
}

type priceListPrepaidResponse struct {
	Data []PriceListPrepaidItem `json:"data"`
}

func (c *Client) GetPriceListPrepaid(ctx context.Context) ([]PriceListPrepaidItem, error) {
	payload := priceListRequest{
		Cmd:      "prepaid",
		Username: c.cfg.DigiflazzUsername,
		Sign:     c.pricelistSign(),
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s%s", c.cfg.DigiflazzBaseURL, "/v1/price-list")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out priceListPrepaidResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("digiflazz pricelist prepaid decode error: %v body=%s", err, string(b))
	}
	return out.Data, nil
}

func (c *Client) Status(ctx context.Context, productCode, customerNo, refID string) (*StatusResult, error) {
	// Return mockup data if mockup mode is enabled
	if c.cfg.MockupMode {
		// For status check, return a mockup status result
		return &StatusResult{
			Status:  "Sukses",
			Message: "Transaksi berhasil",
			RC:      "00",
			Raw: map[string]any{
				"data": map[string]any{
					"status":  "Sukses",
					"message": "Transaksi berhasil",
					"rc":      "00",
				},
			},
		}, nil
	}

	payload := StatusRequest{
		Commands:     "status-pasca",
		Username:     c.cfg.DigiflazzUsername,
		BuyerSKUCode: productCode,
		CustomerNo:   customerNo,
		RefID:        refID,
		Sign:         c.sign(refID),
		Testing:      strings.ToLower(c.cfg.Env) != "production",
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s%s", c.cfg.DigiflazzBaseURL, "/v1/transaction")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		return nil, fmt.Errorf("digiflazz status decode error: %v body=%s", err, string(bodyBytes))
	}
	res := &StatusResult{Raw: raw}
	if data, ok := raw["data"].(map[string]any); ok {
		if v, ok := data["status"].(string); ok {
			res.Status = v
		}
		if v, ok := data["message"].(string); ok {
			res.Message = v
		}
		if v, ok := data["rc"].(string); ok {
			res.RC = v
		}
	}
	return res, nil
}

func (c *Client) Topup(ctx context.Context, productCode, customerNo, refID string, maxPrice int64) (*TopupResult, error) {
	// Return mockup data if mockup mode is enabled
	if c.cfg.MockupMode {
		return getMockupTopup(ctx, productCode, customerNo, refID, maxPrice)
	}

	payload := TopupRequest{
		Username:     c.cfg.DigiflazzUsername,
		BuyerSKUCode: productCode,
		CustomerNo:   customerNo,
		RefID:        refID,
		Sign:         c.sign(refID),
		Testing:      strings.ToLower(c.cfg.Env) != "production",
	}
	if maxPrice > 0 {
		payload.MaxPrice = maxPrice
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s%s", c.cfg.DigiflazzBaseURL, "/v1/transaction")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("digiflazz topup decode error: %v body=%s", err, string(b))
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

func (c *Client) InquiryPLN(ctx context.Context, customerNo string) (*PLNInquiryResult, error) {
	if c.cfg.MockupMode {
		return getMockupPLNInquiry(ctx, customerNo)
	}

	payload := PLNInquiryRequest{
		Username:   c.cfg.DigiflazzUsername,
		CustomerNo: customerNo,
		Sign:       c.signCustomer(customerNo),
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s%s", c.cfg.DigiflazzBaseURL, "/v1/inquiry-pln")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		return nil, fmt.Errorf("digiflazz inquiry-pln decode error: %v body=%s", err, string(bodyBytes))
	}
	res := &PLNInquiryResult{Raw: raw}
	if data, ok := raw["data"].(map[string]any); ok {
		if v, ok := data["status"].(string); ok {
			res.Status = v
		}
		if v, ok := data["message"].(string); ok {
			res.Message = v
		}
		if v, ok := data["rc"].(string); ok {
			res.RC = v
		}
		if v, ok := data["customer_no"].(string); ok {
			res.CustomerNo = v
		}
		if v, ok := data["meter_no"].(string); ok {
			res.MeterNo = v
		}
		if v, ok := data["subscriber_id"].(string); ok {
			res.SubscriberID = v
		}
		if v, ok := data["name"].(string); ok {
			res.Name = v
		}
		if v, ok := data["segment_power"].(string); ok {
			res.SegmentPower = v
		}
	}
	return res, nil
}
