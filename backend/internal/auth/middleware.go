// Package auth xác minh access token HipCore (Bearer) qua JWKS. Có hai
// middleware: User (token người dùng + giải hồ sơ đang hoạt động) và Service
// (token client-credentials nội bộ). Xem docs/02 §2.3.
package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type ctxKey int

const (
	userIDKey ctxKey = iota
	tokenKey
	profileKey
	superuserKey
	gatewayAudienceKey
	serviceKey
	serviceClientKey
)

func UserID(ctx context.Context) string      { v, _ := ctx.Value(userIDKey).(string); return v }
func BearerToken(ctx context.Context) string { v, _ := ctx.Value(tokenKey).(string); return v }
func ProfileUUID(ctx context.Context) string { v, _ := ctx.Value(profileKey).(string); return v }
func IsSuperuser(ctx context.Context) bool   { v, _ := ctx.Value(superuserKey).(bool); return v }
func GatewayAudience(ctx context.Context) string {
	v, _ := ctx.Value(gatewayAudienceKey).(string)
	return v
}
func IsService(ctx context.Context) bool { v, _ := ctx.Value(serviceKey).(bool); return v }
func ServiceClientID(ctx context.Context) string {
	v, _ := ctx.Value(serviceClientKey).(string)
	return v
}

type Authenticator struct {
	jwks       *JWKS
	audience   string
	hipcoreURL string
	log        *slog.Logger
	client     *http.Client

	gatewaySecret string
	gatewayTTL    time.Duration

	mu      sync.Mutex
	meCache map[string]meEntry // sub -> active profile uuid (TTL ngắn)
}

type meEntry struct {
	profileUUID string
	expiresAt   time.Time
}

func NewAuthenticator(jwks *JWKS, audience, hipcoreURL string, log *slog.Logger) *Authenticator {
	return &Authenticator{
		jwks:       jwks,
		audience:   audience,
		hipcoreURL: strings.TrimRight(hipcoreURL, "/"),
		log:        log,
		client:     &http.Client{Timeout: 10 * time.Second},
		meCache:    map[string]meEntry{},
	}
}

func (a *Authenticator) parse(r *http.Request) (*jwt.Token, string, bool) {
	return a.parseWithAudience(r, true)
}

func (a *Authenticator) parseService(r *http.Request) (*jwt.Token, string, bool) {
	return a.parseWithAudience(r, false)
}

func (a *Authenticator) parseWithAudience(r *http.Request, enforceAudience bool) (*jwt.Token, string, bool) {
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if raw == "" || raw == r.Header.Get("Authorization") {
		return nil, "", false
	}
	opts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512"}),
		jwt.WithExpirationRequired(),
	}
	if enforceAudience && a.audience != "" {
		opts = append(opts, jwt.WithAudience(a.audience))
	}
	token, err := jwt.Parse(raw, a.jwks.Keyfunc, opts...)
	if err != nil || !token.Valid {
		return nil, "", false
	}
	return token, raw, true
}

// UserMiddleware: yêu cầu token người dùng (có `sub`) và giải hồ sơ đang hoạt
// động (profile uuid) để gắn vào context.
func (a *Authenticator) UserMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Đường tin-cậy-gateway: request mang danh tính do API gateway ký hợp lệ thì
		// tin ngay (bỏ verify JWKS + bỏ gọi /api/me). profile uuid lấy từ header ký.
		if id, ok := a.gatewayIdentity(r); ok {
			ctx := context.WithValue(r.Context(), userIDKey, id.Sub)
			ctx = context.WithValue(ctx, tokenKey, bearerRaw(r))
			ctx = context.WithValue(ctx, profileKey, id.ProfileUUID)
			ctx = context.WithValue(ctx, superuserKey, id.IsSuper)
			ctx = context.WithValue(ctx, gatewayAudienceKey, id.Audience)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		token, raw, ok := a.parse(r)
		if !ok {
			unauthorized(w, "invalid token")
			return
		}
		sub, err := token.Claims.GetSubject()
		if err != nil || sub == "" {
			unauthorized(w, "token has no subject")
			return
		}
		profileUUID := a.activeProfile(r.Context(), sub, raw)
		ctx := context.WithValue(r.Context(), userIDKey, sub)
		ctx = context.WithValue(ctx, tokenKey, raw)
		ctx = context.WithValue(ctx, profileKey, profileUUID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ServiceMiddleware chỉ chấp nhận client-credentials. HipCore Passport hiện
// đặt sub bằng chính client id cho token máy (một số bản cũ để sub rỗng), nên
// token hợp lệ khi sub rỗng hoặc trùng một audience. Token người dùng có sub
// khác audience và bị chặn. Registry ở transport quyết định client nào được phép.
func (a *Authenticator) ServiceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, raw, ok := a.parseService(r)
		if !ok {
			unauthorized(w, "invalid service token")
			return
		}
		sub, _ := token.Claims.GetSubject()
		aud, err := token.Claims.GetAudience()
		if err != nil || len(aud) == 0 || aud[0] == "" {
			forbidden(w, "not_a_service_token", "Token service thiếu audience")
			return
		}
		if sub != "" {
			matchesClient := false
			for _, clientID := range aud {
				matchesClient = matchesClient || clientID == sub
			}
			if !matchesClient {
				forbidden(w, "not_a_service_token", "Token người dùng không được gọi API service")
				return
			}
		}
		ctx := context.WithValue(r.Context(), serviceKey, true)
		ctx = context.WithValue(ctx, tokenKey, raw)
		ctx = context.WithValue(ctx, serviceClientKey, aud[0])
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// activeProfile giải uuid hồ sơ đang hoạt động qua HipCore /api/me (cache TTL
// ngắn theo sub). Thất bại trả "" — handler báo lỗi nếu cần profile.
func (a *Authenticator) activeProfile(ctx context.Context, sub, token string) string {
	a.mu.Lock()
	if e, ok := a.meCache[sub]; ok && time.Now().Before(e.expiresAt) {
		a.mu.Unlock()
		return e.profileUUID
	}
	a.mu.Unlock()

	uuid := a.fetchActiveProfile(ctx, token)
	if uuid != "" {
		a.mu.Lock()
		a.meCache[sub] = meEntry{profileUUID: uuid, expiresAt: time.Now().Add(60 * time.Second)}
		a.mu.Unlock()
	}
	return uuid
}

func (a *Authenticator) fetchActiveProfile(ctx context.Context, token string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.hipcoreURL+"/api/me", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := a.client.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ""
	}
	var body struct {
		ActiveProfile struct {
			UUID string `json:"uuid"`
		} `json:"active_profile"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return ""
	}
	return body.ActiveProfile.UUID
}

func unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": "unauthorized", "message": msg},
	})
}

func forbidden(w http.ResponseWriter, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}
