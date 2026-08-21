package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// GatewayIdentity là danh tính do API gateway (tm/bff) khẳng định sau khi đã
// xác thực người dùng với HipCore. Service tin danh tính này khi chữ ký HMAC
// khớp GATEWAY_SIGNING_SECRET — thay cho việc tự verify JWT trên hot-path (mô
// hình hybrid). Đường JWKS Bearer vẫn được giữ cho lời gọi service-to-service
// và truy cập trực tiếp.
type GatewayIdentity struct {
	Sub         string
	Email       string
	Name        string
	ProfileUUID string
	IsSuper     bool
	Audience    string
}

// Tên header gateway tiêm vào + tham số ký. PHẢI khớp tm/bff/src/identity.ts.
const (
	gwScheme      = "v1"
	gwHdrUserID   = "X-Gw-User-Id"
	gwHdrEmail    = "X-Gw-User-Email"
	gwHdrName     = "X-Gw-User-Name"
	gwHdrProfile  = "X-Gw-Profile-Uuid"
	gwHdrIsSuper  = "X-Gw-Is-Super"
	gwHdrAudience = "X-Gw-Audience"
	gwHdrIssuedAt = "X-Gw-Issued-At"
	gwHdrSig      = "X-Gw-Signature"

	gatewayDefaultTTL = 60 * time.Second
)

// WithGateway bật đường tin-cậy-gateway với secret chia sẻ. secret rỗng = tắt
// (chỉ còn đường JWKS). Trả về chính Authenticator để tiện nối chuỗi.
func (a *Authenticator) WithGateway(secret string) *Authenticator {
	a.gatewaySecret = secret
	a.gatewayTTL = gatewayDefaultTTL
	return a
}

// gatewayIdentity verify header danh tính do gateway ký trên request hiện tại.
func (a *Authenticator) gatewayIdentity(r *http.Request) (*GatewayIdentity, bool) {
	return verifyGateway(r, a.gatewaySecret, a.gatewayTTL)
}

// verifyGateway kiểm tra chữ ký HMAC-SHA256 trên bộ header danh tính và cửa sổ
// chống replay. Trả về (identity, true) khi hợp lệ.
func verifyGateway(r *http.Request, secret string, ttl time.Duration) (*GatewayIdentity, bool) {
	if secret == "" {
		return nil, false
	}
	sig := r.Header.Get(gwHdrSig)
	issuedAt := r.Header.Get(gwHdrIssuedAt)
	if sig == "" || issuedAt == "" {
		return nil, false
	}
	ts, err := strconv.ParseInt(issuedAt, 10, 64)
	if err != nil {
		return nil, false
	}
	if d := time.Since(time.Unix(ts, 0)); d > ttl || d < -ttl {
		return nil, false // ngoài cửa sổ cho phép → từ chối
	}

	id := &GatewayIdentity{
		Sub:         r.Header.Get(gwHdrUserID),
		Email:       r.Header.Get(gwHdrEmail),
		Name:        r.Header.Get(gwHdrName),
		ProfileUUID: r.Header.Get(gwHdrProfile),
		IsSuper:     r.Header.Get(gwHdrIsSuper) == "true",
		Audience:    r.Header.Get(gwHdrAudience),
	}
	if id.Sub == "" {
		return nil, false
	}

	fields := []string{gwScheme, id.Sub, id.Email, id.Name, id.ProfileUUID}
	if id.Audience != "" {
		fields = append(fields, strconv.FormatBool(id.IsSuper), id.Audience)
	}
	fields = append(fields, issuedAt)
	canonical := strings.Join(fields, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(canonical))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return nil, false
	}
	return id, true
}

// bearerRaw lấy token Bearer thô (KHÔNG verify) — dùng làm best-effort khi đi
// qua đường gateway, để code đọc BearerToken(ctx) cho lời gọi nội bộ vẫn chạy.
func bearerRaw(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if raw := strings.TrimPrefix(h, "Bearer "); raw != h {
		return raw
	}
	return ""
}
