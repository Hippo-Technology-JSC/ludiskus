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
const jwksMinRefresh = 30 * time.Second

type JWKS struct {
	url    string
	client *http.Client

	mu          sync.RWMutex
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time
	lastAttempt time.Time
	refreshMu   sync.Mutex
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

// cachedKey reads the key and refresh state together. Passport tokens have no kid,
// so a single cached key must be selected before deciding to fetch JWKS.
func (j *JWKS) cachedKey(kid string) (*rsa.PublicKey, bool, bool, bool) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	key, found := j.keys[kid]
	if !found && kid == "" && len(j.keys) == 1 {
		for _, only := range j.keys {
			key, found = only, true
		}
	}
	return key, found, time.Since(j.fetchedAt) > time.Hour, time.Since(j.lastAttempt) < jwksMinRefresh
}

// Keyfunc caches Passport's kid-less key, serializes refreshes, and limits
// unknown-kid refresh attempts (including failed requests) to one per 30s.
func (j *JWKS) Keyfunc(token *jwt.Token) (any, error) {
	if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
		return nil, fmt.Errorf("unexpected signing method %s", token.Method.Alg())
	}
	kid, _ := token.Header["kid"].(string)
	key, found, stale, recent := j.cachedKey(kid)
	if found && (!stale || recent) {
		return key, nil
	}
	if !found && recent {
		return nil, fmt.Errorf("no matching JWK for kid %q", kid)
	}

	// A second caller rechecks after the first refresh completes. This avoids a
	// burst of simultaneous requests on cold start or key rotation.
	j.refreshMu.Lock()
	defer j.refreshMu.Unlock()
	key, found, stale, recent = j.cachedKey(kid)
	if found && (!stale || recent) {
		return key, nil
	}
	if !found && recent {
		return nil, fmt.Errorf("no matching JWK for kid %q", kid)
	}
	j.mu.Lock()
	j.lastAttempt = time.Now()
	j.mu.Unlock()
	if err := j.refresh(); err != nil {
		if found { // Keep a known key usable during a temporary JWKS outage.
			return key, nil
		}
		return nil, err
	}
	key, found, _, _ = j.cachedKey(kid)
	if found {
		return key, nil
	}
	return nil, fmt.Errorf("no matching JWK for kid %q", kid)
}
