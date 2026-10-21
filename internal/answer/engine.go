// Package answer owns the configurable answer-generation provider chain. It
// never talks to Chaoxing; the workdraft adapter supplies parsed questions and
// persists the normalized result.
package answer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/persist"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

var ErrNoProvider = errors.New("answer provider unavailable")

type Question struct {
	ID      string
	Title   string
	Type    string
	Options []string
}

type Answer struct {
	QuestionID string  `json:"question_id"`
	Type       string  `json:"type"`
	Text       string  `json:"answer"`
	Source     string  `json:"source"`
	Confidence float64 `json:"confidence"`
}

type Provider interface {
	Name() string
	Query(ctx context.Context, q Question) (string, float64, error)
}

type Engine struct {
	providers     []Provider
	cache         *Cache
	minConfidence float64
	mu            sync.Mutex
}

func NewEngine(providers []Provider, cachePath string, minConfidence float64) (*Engine, error) {
	if minConfidence <= 0 || minConfidence > 1 {
		minConfidence = 0.75
	}
	var cache *Cache
	if strings.TrimSpace(cachePath) != "" {
		var err error
		cache, err = OpenCache(cachePath)
		if err != nil {
			return nil, err
		}
	}
	return &Engine{providers: append([]Provider(nil), providers...), cache: cache, minConfidence: minConfidence}, nil
}

func (e *Engine) Enabled() bool { return e != nil && len(e.providers) > 0 }

func (e *Engine) Answer(ctx context.Context, q Question) (Answer, error) {
	if !e.Enabled() {
		return Answer{}, ErrNoProvider
	}
	key := questionKey(q)
	if e.cache != nil {
		if cached, ok := e.cache.Get(key); ok && cached.Confidence >= e.minConfidence {
			cached.QuestionID, cached.Type = q.ID, q.Type
			return cached, nil
		}
	}
	var lastErr error
	for _, provider := range e.providers {
		if err := ctx.Err(); err != nil {
			return Answer{}, err
		}
		raw, confidence, err := provider.Query(ctx, q)
		if err != nil || strings.TrimSpace(raw) == "" {
			lastErr = err
			continue
		}
		normalized, err := Normalize(q, raw)
		if err != nil {
			lastErr = err
			continue
		}
		if confidence <= 0 {
			confidence = 0.8
		}
		if confidence > 1 {
			confidence = 1
		}
		out := Answer{QuestionID: q.ID, Type: q.Type, Text: normalized, Source: provider.Name(), Confidence: confidence}
		if confidence >= e.minConfidence {
			if e.cache != nil {
				_ = e.cache.Put(key, out)
			}
			return out, nil
		}
	}
	if lastErr == nil {
		lastErr = ErrNoProvider
	}
	return Answer{}, lastErr
}

func FromUpstream(q upstream.WorkQuestion) Question {
	return Question{ID: q.ID, Title: q.Title, Type: q.Type, Options: append([]string(nil), q.Options...)}
}

func questionKey(q Question) string {
	options := append([]string(nil), q.Options...)
	sort.Strings(options)
	sum := sha256.Sum256([]byte(q.Type + "\x00" + strings.TrimSpace(q.Title) + "\x00" + strings.Join(options, "\n")))
	return hex.EncodeToString(sum[:])
}

type Cache struct {
	path string
	mu   sync.Mutex
	data map[string]Answer
}

type cacheFile struct {
	Version int               `json:"version"`
	Answers map[string]Answer `json:"answers"`
}

func OpenCache(path string) (*Cache, error) {
	c := &Cache{path: path, data: map[string]Answer{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("answer cache unavailable")
	}
	if len(b) > 2<<20 {
		return nil, fmt.Errorf("answer cache exceeds limit")
	}
	var state cacheFile
	if json.Unmarshal(b, &state) != nil || state.Version != 1 || state.Answers == nil {
		return nil, fmt.Errorf("answer cache invalid")
	}
	c.data = state.Answers
	return c, nil
}

func (c *Cache) Get(key string) (Answer, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out, ok := c.data[key]
	return out, ok
}

func (c *Cache) Put(key string, value Answer) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	value.QuestionID = ""
	c.data[key] = value
	b, err := json.Marshal(cacheFile{Version: 1, Answers: c.data})
	if err != nil || len(b) > 2<<20 {
		return fmt.Errorf("answer cache write failed")
	}
	if err := persist.Write(c.path, b, 0o600); err != nil {
		return fmt.Errorf("answer cache write failed")
	}
	return nil
}

func (e *Engine) LastUsed() time.Time { return time.Time{} }
