package domain

import "time"

type TransactionAction string

const (
    ActionInquiry TransactionAction = "inquiry"
    ActionPayment TransactionAction = "payment"
)

type TransactionStatus string

const (
    StatusSuccess TransactionStatus = "SUKSES"
    StatusPending TransactionStatus = "PENDING"
    StatusFailed  TransactionStatus = "GAGAL"
)

type Transaction struct {
    RefID         string
    Action        TransactionAction
    ProductCode   string
    CustomerNo    string
    BillAmount    int64
    AdminFee      int64
    Margin        int64
    SellingPrice  int64
    ExternalStatus string
    ExternalMessage string
    RawData       map[string]any
    CreatedAt     time.Time
    UpdatedAt     time.Time
}


