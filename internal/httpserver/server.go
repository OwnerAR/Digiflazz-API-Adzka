package httpserver

import (
    "context"
    "fmt"
    "net/http"
    "time"

    "digiflazz-api/internal/config"
    "digiflazz-api/internal/digiflazz"
    "digiflazz-api/internal/handlers"
    "digiflazz-api/internal/logging"
    "digiflazz-api/internal/otomax"
    mssqlstore "digiflazz-api/internal/storage/mssql"
    sqlitestore "digiflazz-api/internal/storage/sqlite"
)

type Server struct {
    cfg    *config.Config
    logger *logging.Logger
    srv    *http.Server
}

func NewServer(cfg *config.Config, logger *logging.Logger) *Server {
    mux := http.NewServeMux()

    // health
    mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte(`{"status":"ok"}`))
    })

    // Dependencies
    sqlite, err := sqlitestore.New(cfg.SQLiteDSN)
    if err != nil {
        logger.Errorf("sqlite init error: %v", err)
    }
    mssql, err := mssqlstore.New(cfg.OtomaxMSSQLDSN)
    if err != nil {
        logger.Errorf("mssql init error: %v", err)
    } else {
        // Log connection status
        ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
        if err := mssql.PingContext(ctx); err != nil {
            logger.Errorf("mssql connection failed: %v", err)
        } else {
            logger.Infof("mssql connected successfully")
        }
        cancel()
    }
    dgf := digiflazz.New(cfg)
    otomaxClient := otomax.New(cfg)

    // OtomaX endpoint (no security middleware for local app)
    otomaxHandler := handlers.NewOtomaxHandler(cfg, logger, sqlite, mssql, dgf, otomaxClient)
    mux.Handle("/api/otomax", otomaxHandler)

    // Product sync: Digiflazz pasca price list -> OtomaX parsing
    productsHandler := handlers.NewProductSyncHandler(cfg, logger, mssql, dgf)
    mux.Handle("/api/otomax/products/pasca", productsHandler)
    prepaidProductsHandler := handlers.NewProductPrepaidSyncHandler(cfg, logger, mssql, dgf)
    mux.Handle("/api/otomax/products/prepaid", prepaidProductsHandler)

    // Prepaid endpoint
    prepaidHandler := handlers.NewPrepaidHandler(cfg, logger, sqlite, mssql, dgf)
    mux.Handle("/api/otomax/prepaid", prepaidHandler)

    // DigiFlazz Seller API (incoming topup): validasi sign, price >= harga_jual, forward ke OtomaX InsertInbox
    sellerTopupHandler := handlers.NewSellerTopupHandler(cfg, logger, sqlite, mssql, otomaxClient)
    mux.Handle("/api/seller/topup", sellerTopupHandler)

    // Background scheduler for product sync
    if cfg.ProductSyncEnabled {
        interval := time.Duration(cfg.ProductSyncIntervalMin) * time.Minute
        if interval <= 0 { interval = 60 * time.Minute }
        go func() {
            ticker := time.NewTicker(interval)
            defer ticker.Stop()
            // run once at startup (both pasca and prepaid)
            runProductSyncOnce(cfg, logger, mssql, dgf)
            runProductSyncPrepaidOnce(cfg, logger, mssql, dgf)
            for range ticker.C {
                runProductSyncOnce(cfg, logger, mssql, dgf)
                runProductSyncPrepaidOnce(cfg, logger, mssql, dgf)
            }
        }()
        logger.Infof("product sync scheduler enabled; interval=%v", time.Duration(cfg.ProductSyncIntervalMin)*time.Minute)
    }

    s := &http.Server{
        Addr:              fmt.Sprintf(":%s", cfg.ServerPort),
        Handler:           mux,
        ReadTimeout:       time.Duration(cfg.RequestTimeoutMs) * time.Millisecond,
        ReadHeaderTimeout: time.Duration(cfg.RequestTimeoutMs) * time.Millisecond,
        WriteTimeout:      time.Duration(cfg.RequestTimeoutMs) * time.Millisecond,
        IdleTimeout:       60 * time.Second,
    }

    return &Server{cfg: cfg, logger: logger, srv: s}
}

func (s *Server) Start() error {
    return s.srv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
    return s.srv.Shutdown(ctx)
}

func runProductSyncOnce(cfg *config.Config, logger *logging.Logger, mssql *mssqlstore.Client, dgf *digiflazz.Client) {
    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()
    totalPricelist, totalProducts, inserted, err := handlers.RunProductSync(ctx, cfg, logger, mssql, dgf)
    if err != nil {
        logger.Errorf("product sync failed: %v", err)
        return
    }
    logger.Infof("product sync completed: pricelist=%d otomax_products=%d inserted_parsing=%d", totalPricelist, totalProducts, inserted)
}

func runProductSyncPrepaidOnce(cfg *config.Config, logger *logging.Logger, mssql *mssqlstore.Client, dgf *digiflazz.Client) {
    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()
    totalPricelist, totalProducts, inserted, err := handlers.RunProductSyncPrepaid(ctx, cfg, logger, mssql, dgf)
    if err != nil {
        logger.Errorf("product prepaid sync failed: %v", err)
        return
    }
    logger.Infof("product prepaid sync completed: pricelist=%d otomax_products=%d inserted_parsing=%d", totalPricelist, totalProducts, inserted)
}


