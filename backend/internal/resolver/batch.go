package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"ludiskus/internal/domain"
	"net/http"
	"strings"
)

// ResolveBatch groups cache misses by provider; unsupported batch endpoints fall
// back to single-resource resolution. Provider failures never trigger N retries.
func (r *Resolver) ResolveBatch(ctx context.Context, refs []domain.ResourceRef) (map[string]*Result, map[string]error) {
	out := map[string]*Result{}
	skipped := map[string]error{}
	groups := map[string][]domain.ResourceRef{}
	seen := map[string]bool{}
	if len(refs) > r.cfg.CommentBatchMax {
		for _, ref := range refs {
			skipped[ref.String()] = ErrInvalid
		}
		return out, skipped
	}
	for _, ref := range refs {
		key := ref.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		if ref.Validate() != nil {
			skipped[key] = ErrInvalid
			continue
		}
		if ref.Service == "ludiskus" && r.local != nil {
			v, e := r.Resolve(ctx, ref)
			if e != nil {
				skipped[key] = e
			} else {
				out[key] = v
			}
			continue
		}
		if r.redis != nil {
			if raw, e := r.redis.Get(ctx, cacheKey(ref)).Bytes(); e == nil {
				var v Result
				if json.Unmarshal(raw, &v) == nil && validateResult(ref, &v) == nil {
					out[key] = &v
					continue
				}
			}
		}
		groups[ref.Service] = append(groups[ref.Service], ref)
	}
	for code, batch := range groups {
		svc, e := r.repo.GetCommentService(ctx, code)
		if e != nil || !svc.IsActive {
			for _, ref := range batch {
				skipped[ref.String()] = ErrNotFound
			}
			continue
		}
		paths := []string{svc.ContextPath}
		if svc.ContextPath == "" {
			paths = []string{"resource-context", "interaction-context"}
		}
		var values []*Result
		status := 0
		for _, path := range paths {
			values, status, e = r.callBatch(ctx, svc.BaseURL, path, batch)
			if e != nil || (status != 404 && status != 405) {
				break
			}
		}
		if e == nil && (status == 404 || status == 405) {
			for _, ref := range batch {
				v, err := r.Resolve(ctx, ref)
				if err != nil {
					skipped[ref.String()] = err
				} else {
					out[ref.String()] = v
				}
			}
			continue
		}
		if e != nil || status != 200 {
			err := ErrInvalid
			if e != nil || status >= 500 {
				err = ErrUnavailable
			}
			for _, ref := range batch {
				skipped[ref.String()] = err
			}
			continue
		}
		wanted := map[string]domain.ResourceRef{}
		for _, ref := range batch {
			wanted[ref.String()] = ref
			skipped[ref.String()] = ErrNotFound
		}
		for _, v := range values {
			if v == nil {
				continue
			}
			ref := domain.ResourceRef{Service: code, Type: v.Type, ID: v.ID}
			key := ref.String()
			if _, ok := wanted[key]; !ok {
				continue
			}
			if err := validateResult(ref, v); err != nil {
				skipped[key] = err
				continue
			}
			out[key] = v
			delete(skipped, key)
			if r.redis != nil {
				raw, _ := json.Marshal(v)
				_ = r.redis.Set(ctx, cacheKey(ref), raw, r.cfg.CommentTargetTTL).Err()
			}
		}
	}
	return out, skipped
}

func (r *Resolver) callBatch(ctx context.Context, base, path string, refs []domain.ResourceRef) ([]*Result, int, error) {
	token, e := r.accessToken(ctx)
	if e != nil {
		return nil, 0, e
	}
	raw, e := json.Marshal(map[string]any{"refs": refs})
	if e != nil {
		return nil, 0, e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/api/v1/s2s/"+path+":batch", bytes.NewReader(raw))
	if e != nil {
		return nil, 0, e
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, e := r.http.Do(req)
	if e != nil {
		return nil, 0, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, res.StatusCode, nil
	}
	var payload struct {
		Data []*Result `json:"data"`
	}
	e = json.NewDecoder(res.Body).Decode(&payload)
	return payload.Data, res.StatusCode, e
}
