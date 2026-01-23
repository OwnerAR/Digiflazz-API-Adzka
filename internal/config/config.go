package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Env        string
	ServerPort string

	// SQLite
	SQLiteDSN string

	// MSSQL OtomaX (optional, untuk sinkronisasi produk)
	OtomaxMSSQLDSN    string
	OtomaxKodeModul   string
	OtomaxCallbackURL string

	// OtomaX API
	OtomaxAPIBaseURL string
	OtomaxAppID      string
	OtomaxAppKey     string
	OtomaxDevKey     string

	// Security
	AllowedSourceIPs      []string
	OtomaxSignatureSecret string

	// Pricing
	DefaultAdminFee int64
	DefaultMargin   int64

	// HTTP
	RequestTimeoutMs  int
	CallbackTimeoutMs int // Timeout khusus untuk callback OtomaX (default: 30 detik)

	// Digiflazz
	DigiflazzBaseURL         string
	DigiflazzUsername        string
	DigiflazzAPIKey          string
	DigiflazzSignatureSecret string

	// Schedulers
	ProductSyncEnabled     bool
	ProductSyncIntervalMin int

	// Mockup/Testing
	MockupMode         bool
	UpdateProductPrice bool
	DefaultMarkup      int64
}

func Load() (*Config, error) {
	// Load .env automatically (ignore error if file missing)
	if err := godotenv.Load(); err != nil {
		// Log if .env file not found (only in development or debug mode)
		if os.Getenv("DEBUG_CONFIG") == "true" {
			fmt.Printf("[DEBUG] .env file not found or error loading: %v\n", err)
		}
	}

	cfg := &Config{}
	cfg.Env = getEnv("APP_ENV", "development")
	cfg.ServerPort = getEnv("SERVER_PORT", "8080")

	cfg.SQLiteDSN = getEnv("SQLITE_DSN", "file:digiflazz.db?_busy_timeout=5000&_fk=1")
	cfg.OtomaxMSSQLDSN = getEnv("OTOMAX_MSSQL_DSN", "")
	cfg.OtomaxKodeModul = getEnv("OTOMAX_KODE_MODUL", "")
	cfg.OtomaxCallbackURL = getEnv("OTOMAX_CALLBACK_URL", "")

	// OtomaX API
	cfg.OtomaxAPIBaseURL = getEnv("OTOMAX_API_BASE_URL", "")
	cfg.OtomaxAppID = getEnv("OTOMAX_APP_ID", "")
	cfg.OtomaxAppKey = getEnv("OTOMAX_APP_KEY", "")
	cfg.OtomaxDevKey = getEnv("OTOMAX_DEV_KEY", "")

	cfg.AllowedSourceIPs = splitCSV(getEnv("ALLOWED_SOURCE_IPS", ""))
	cfg.OtomaxSignatureSecret = getEnv("OTOMAX_SIGNATURE_SECRET", "")

	cfg.DefaultAdminFee = getEnvInt64("DEFAULT_ADMIN_FEE", 0)
	cfg.DefaultMargin = getEnvInt64("DEFAULT_MARGIN", 0)
	cfg.DefaultMarkup = getEnvInt64("DEFAULT_MARKUP", 0)
	cfg.RequestTimeoutMs = getEnvInt("REQUEST_TIMEOUT_MS", 15000)
	cfg.CallbackTimeoutMs = getEnvInt("CALLBACK_TIMEOUT_MS", 30000) // Default 30 detik untuk callback

	cfg.DigiflazzBaseURL = getEnv("DIGIFLAZZ_BASE_URL", "")
	cfg.DigiflazzUsername = getEnv("DIGIFLAZZ_USERNAME", "")
	cfg.DigiflazzAPIKey = getEnv("DIGIFLAZZ_API_KEY", "")
	cfg.DigiflazzSignatureSecret = getEnv("DIGIFLAZZ_SIGNATURE_SECRET", "")

	// schedulers
	cfg.ProductSyncEnabled = parseBool(getEnv("PRODUCT_SYNC_ENABLED", "false"))
	cfg.ProductSyncIntervalMin = getEnvInt("PRODUCT_SYNC_INTERVAL_MINUTES", 60)

	// mockup mode
	mockupEnvValue := getEnv("MOCKUP_MODE", "false")
	cfg.MockupMode = parseBool(mockupEnvValue)
	// Debug: log the raw env value and parsed result (only in development or if enabled via env)
	if cfg.IsDevelopment() || os.Getenv("DEBUG_CONFIG") == "true" {
		fmt.Printf("[DEBUG] MOCKUP_MODE env value: %q, parsed: %v\n", mockupEnvValue, cfg.MockupMode)
	}
	cfg.UpdateProductPrice = parseBool(getEnv("UPDATE_PRODUCT_PRICE", "false"))
	// Skip Digiflazz config validation if mockup mode is enabled
	if !cfg.MockupMode {
		if cfg.DigiflazzBaseURL == "" || cfg.DigiflazzUsername == "" || cfg.DigiflazzAPIKey == "" {
			return nil, errors.New("missing Digiflazz configuration: DIGIFLAZZ_BASE_URL, DIGIFLAZZ_USERNAME, DIGIFLAZZ_API_KEY are required")
		}
		if cfg.OtomaxSignatureSecret == "" {
			return nil, errors.New("missing OTOMAX_SIGNATURE_SECRET for request verification")
		}
	}

	// Validate OtomaX API config (required unless mockup mode)
	if !cfg.MockupMode {
		if cfg.OtomaxAPIBaseURL == "" || cfg.OtomaxAppID == "" || cfg.OtomaxAppKey == "" || cfg.OtomaxDevKey == "" {
			return nil, errors.New("missing OtomaX API configuration: OTOMAX_API_BASE_URL, OTOMAX_APP_ID, OTOMAX_APP_KEY, OTOMAX_DEV_KEY are required")
		}
	}

	return cfg, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func getEnvInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i
		}
	}
	return def
}

// parseBool parses a string as boolean (case-insensitive, trims whitespace)
// Returns true only if value is "true", "1", "yes", "on" (case-insensitive)
func parseBool(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return s == "true" || s == "1" || s == "yes" || s == "on"
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// IsDevelopment returns true if the current environment is development
func (c *Config) IsDevelopment() bool {
	return strings.ToLower(c.Env) == "development"
}
