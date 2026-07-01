package auth

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWKS client: tải khóa công khai từ HipCore, cache và tự refresh khi gặp kid
// lạ (docs/02 §2.3).
type JWKS struct {
	url    string
	client *http.Client

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

func NewJWKS(url string) *JWKS {
	return &JWKS{
		url:    url,
		client: &http.Client{Timeout: 10 * time.Second},
		keys:   map[string]*rsa.PublicKey{},
	}
}

type jwksDoc struct {
	Keys []struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

func (j *JWKS) refresh() error {
	res, err := j.client.Get(j.url)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch jwks: status %d", res.StatusCode)
	}

	var doc jwksDoc
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		return fmt.Errorf("decode jwks: %w", err)
	}

	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		nb, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eb, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(nb),
			E: int(new(big.Int).SetBytes(eb).Int64()),
		}
	}
	if len(keys) == 0 {
		return fmt.Errorf("jwks has no usable RSA keys")
	}

	j.mu.Lock()
	j.keys = keys
	j.fetchedAt = time.Now()
	j.mu.Unlock()
	return nil
}

func (j *JWKS) key(kid string) (*rsa.PublicKey, bool) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	k, ok := j.keys[kid]
	return k, ok
}

func (j *JWKS) stale() bool {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return time.Since(j.fetchedAt) > time.Hour
}

// Keyfunc dùng cho jwt.Parse. Token Passport có thể không đặt kid khi chỉ có
// một khóa — khi đó dùng khóa duy nhất trong bộ.
func (j *JWKS) Keyfunc(token *jwt.Token) (any, error) {
	if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
		return nil, fmt.Errorf("unexpected signing method %s", token.Method.Alg())
	}
	kid, _ := token.Header["kid"].(string)

	if j.stale() {
		_ = j.refresh()
	}
	if k, ok := j.key(kid); ok {
		return k, nil
	}
	if err := j.refresh(); err != nil {
		return nil, err
	}
	if k, ok := j.key(kid); ok {
		return k, nil
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	if kid == "" && len(j.keys) == 1 {
		for _, k := range j.keys {
			return k, nil
		}
	}
	return nil, fmt.Errorf("no matching JWK for kid %q", kid)
}
