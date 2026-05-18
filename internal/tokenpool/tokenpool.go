// Package tokenpool implements round-robin selection of upstream Qwen tokens
// with per-token cooldown after failure.
package tokenpool

import (
	"errors"
	"sync"
	"time"

	"github.com/keaume34/qwen2api/internal/config"
)

// ErrNoToken is returned when no token is currently available.
var ErrNoToken = errors.New("no Qwen token available")

type slot struct {
	token       config.Token
	cooldownEnd time.Time
	hits        int64
	failures    int64
}

// Pool selects the next healthy token. Safe for concurrent use.
type Pool struct {
	mu       sync.Mutex
	slots    []*slot
	cursor   int
	cooldown time.Duration
}

// New constructs a Pool from the given tokens.
func New(tokens []config.Token, cooldown time.Duration) *Pool {
	slots := make([]*slot, 0, len(tokens))
	for _, t := range tokens {
		if t.Value == "" {
			continue
		}
		slots = append(slots, &slot{token: t})
	}
	return &Pool{slots: slots, cooldown: cooldown}
}

// Size returns the number of tokens currently in the pool.
func (p *Pool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.slots)
}

// Take returns the next healthy token. The returned name is the human-friendly
// identifier (or "" if not set).
func (p *Pool) Take() (config.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.slots) == 0 {
		return config.Token{}, ErrNoToken
	}
	now := time.Now()
	for i := 0; i < len(p.slots); i++ {
		idx := (p.cursor + i) % len(p.slots)
		s := p.slots[idx]
		if now.Before(s.cooldownEnd) {
			continue
		}
		p.cursor = (idx + 1) % len(p.slots)
		s.hits++
		return s.token, nil
	}
	return config.Token{}, ErrNoToken
}

// MarkBad puts the given token on cooldown.
func (p *Pool) MarkBad(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	end := time.Now().Add(p.cooldown)
	for _, s := range p.slots {
		if s.token.Value == token {
			s.cooldownEnd = end
			s.failures++
			return
		}
	}
}

// Replace swaps the value of a slot identified by oldValue with newValue. Returns
// true if a slot was found.
func (p *Pool) Replace(oldValue, newValue string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.slots {
		if s.token.Value == oldValue {
			s.token.Value = newValue
			s.cooldownEnd = time.Time{}
			return true
		}
	}
	return false
}

// Status describes one token's current state.
type Status struct {
	Name        string `json:"name,omitempty"`
	Value       string `json:"value"`
	OnCooldown  bool   `json:"on_cooldown"`
	CooldownEnd int64  `json:"cooldown_end,omitempty"`
	Hits        int64  `json:"hits"`
	Failures    int64  `json:"failures"`
}

// Statuses returns a snapshot of all tokens.
func (p *Pool) Statuses() []Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	out := make([]Status, 0, len(p.slots))
	for _, s := range p.slots {
		st := Status{
			Name:     s.token.Name,
			Value:    s.token.Value,
			Hits:     s.hits,
			Failures: s.failures,
		}
		if now.Before(s.cooldownEnd) {
			st.OnCooldown = true
			st.CooldownEnd = s.cooldownEnd.Unix()
		}
		out = append(out, st)
	}
	return out
}
