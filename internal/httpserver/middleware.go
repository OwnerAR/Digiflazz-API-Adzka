package httpserver

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "net"
    "net/http"
    "strings"

    "digiflazz-api/internal/config"
    "digiflazz-api/internal/logging"
)

func withSecurity(cfg *config.Config, logger *logging.Logger, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Log incoming IP and path (supports common proxy headers)
        clientIP := clientIPFromRequest(r)
        logger.Infof("incoming request ip=%s method=%s path=%s", clientIP, r.Method, r.URL.Path)

        // Enforce GET for OtomaX
        if r.Method != http.MethodGet {
            http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
            return
        }

        // IP whitelist
        if len(cfg.AllowedSourceIPs) > 0 {
            ip := clientIP
            if !contains(cfg.AllowedSourceIPs, ip) {
                http.Error(w, "forbidden", http.StatusForbidden)
                return
            }
        }

        // Signature check: sign = HMAC-SHA256(secret, ref_id+ts)
        refID := r.URL.Query().Get("ref_id")
        ts := r.URL.Query().Get("ts")
        provided := r.URL.Query().Get("sign")
        if refID == "" || ts == "" || provided == "" {
            http.Error(w, "missing signature parameters", http.StatusBadRequest)
            return
        }
        mac := hmac.New(sha256.New, []byte(cfg.OtomaxSignatureSecret))
        mac.Write([]byte(refID + ts))
        expected := hex.EncodeToString(mac.Sum(nil))
        // Compare in constant time
        if !hmac.Equal([]byte(strings.ToLower(provided)), []byte(strings.ToLower(expected))) {
            http.Error(w, "invalid signature", http.StatusUnauthorized)
            return
        }

        next.ServeHTTP(w, r)
    })
}

func contains(list []string, v string) bool {
    for _, it := range list {
        if it == v {
            return true
        }
    }
    return false
}

func clientIPFromRequest(r *http.Request) string {
    // X-Forwarded-For may contain multiple IPs: client, proxy1, proxy2 ...
    if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
        parts := strings.Split(xff, ",")
        for _, p := range parts {
            p = strings.TrimSpace(p)
            if p != "" {
                return p
            }
        }
    }
    if rip := r.Header.Get("X-Real-IP"); rip != "" {
        return strings.TrimSpace(rip)
    }
    if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
        return ip
    }
    return r.RemoteAddr
}


