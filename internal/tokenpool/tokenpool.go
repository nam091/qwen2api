// Package tokenpool implements round-robin selection of upstream Qwen tokens
// with per-token exponential backoff cooldown after failure.
package tokenpool

import (
	"errors"
	"math"
	"sync"
	"time"

	"github.com/keaume34/qwen2api/internal/config"
)

// ErrNoToken is returned when no token is currently available.
var ErrNoToken = errors.New("no Qwen token available")

const (
	maxCooldown = 10 * time.Minute
	minCooldown = 5 * time.Second
)

type slot struct {
	token            config.Token
	cooldownEnd      time.Time
	consecutiveFails int
	hits             int64
	failures         int64
}

// effectiveCooldown returns the cooldown duration with exponential backoff.
func (s *slot) effectiveCooldown(base time.Duration) time.Duration {
	if s.consecutiveFails <= 1 {
		return base
	}
	multiplier := math.Pow(2, float64(s.consecutiveFails-1))
	cd := time.Duration(float64(base) * multiplier)
	if cd > maxCooldown {
		cd = maxCooldown
	}
	if cd < minCooldown {
		cd = minCooldown
	}
	return cd
}

// Pool selects the next healthy token. Safe for concurrent use.
type Pool struct {
	mu        sync.Mutex
	slots     []*slot
	slotIndex map[string]*slot // O(1) lookup by token value
	cursor    int
	cooldown  time.Duration
}

// New constructs a Pool from the given tokens.
func New(tokens []config.Token, cooldown time.Duration) *Pool {
	if cooldown < minCooldown {
		cooldown = minCooldown
	}
	slots := make([]*slot, 0, len(tokens))
	index := make(map[string]*slot, len(tokens))
	for _, t := range tokens {
		if t.Value == "" {
			continue
		}
		s := &slot{token: t}
		slots = append(slots, s)
		index[t.Value] = s
	}
	return &Pool{slots: slots, slotIndex: index, cooldown: cooldown}
}

// SetTokens updates the active pool of tokens thread-safely.
func (p *Pool) SetTokens(tokens []config.Token) {
	p.mu.Lock()
	defer p.mu.Unlock()

	newSlots := make([]*slot, 0, len(tokens))
	newIndex := make(map[string]*slot, len(tokens))
	for _, t := range tokens {
		if t.Value == "" {
			continue
		}
		if existing, ok := p.slotIndex[t.Value]; ok {
			existing.token.Name = t.Name
			newSlots = append(newSlots, existing)
			newIndex[t.Value] = existing
		} else {
			s := &slot{token: t}
			newSlots = append(newSlots, s)
			newIndex[t.Value] = s
		}
	}
	p.slots = newSlots
	p.slotIndex = newIndex
	if len(p.slots) > 0 {
		p.cursor = p.cursor % len(p.slots)
	} else {
		p.cursor = 0
	}
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

// TakeWithWait tries to take a token, waiting up to maxWait if all tokens are
// on cooldown. Returns ErrNoToken if no token becomes available within maxWait.
func (p *Pool) TakeWithWait(maxWait time.Duration) (config.Token, error) {
	deadline := time.Now().Add(maxWait)
	for {
		t, err := p.Take()
		if err == nil {
			return t, nil
		}
		p.mu.Lock()
		earliest := p.earliestCooldownEnd()
		p.mu.Unlock()
		if earliest.IsZero() || earliest.After(deadline) {
			return config.Token{}, ErrNoToken
		}
		wait := time.Until(earliest)
		if wait <= 0 {
			continue
		}
		if time.Now().Add(wait).After(deadline) {
			wait = time.Until(deadline)
		}
		time.Sleep(wait)
		if time.Now().After(deadline) {
			return config.Token{}, ErrNoToken
		}
	}
}

// earliestCooldownEnd returns the earliest time a token will be available.
// Caller must hold p.mu.
func (p *Pool) earliestCooldownEnd() time.Time {
	var earliest time.Time
	now := time.Now()
	for _, s := range p.slots {
		if s.cooldownEnd.After(now) {
			if earliest.IsZero() || s.cooldownEnd.Before(earliest) {
				earliest = s.cooldownEnd
			}
		}
	}
	return earliest
}

// MarkBad puts the given token on cooldown with exponential backoff.
func (p *Pool) MarkBad(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.slotIndex[token]; ok {
		s.consecutiveFails++
		s.failures++
		cd := s.effectiveCooldown(p.cooldown)
		s.cooldownEnd = time.Now().Add(cd)
	}
}

// MarkGood resets the consecutive failure count for a token (call on success).
func (p *Pool) MarkGood(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.slotIndex[token]; ok {
		s.consecutiveFails = 0
	}
}

// Replace swaps the value of a slot identified by oldValue with newValue. Returns
// true if a slot was found.
func (p *Pool) Replace(oldValue, newValue string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.slotIndex[oldValue]; ok {
		delete(p.slotIndex, oldValue)
		s.token.Value = newValue
		s.cooldownEnd = time.Time{}
		s.consecutiveFails = 0
		p.slotIndex[newValue] = s
		return true
	}
	return false
}

// Status describes one token's current state.
type Status struct {
	Name             string `json:"name,omitempty"`
	Value            string `json:"value"`
	OnCooldown       bool   `json:"on_cooldown"`
	CooldownEnd      int64  `json:"cooldown_end,omitempty"`
	CooldownRemains  int    `json:"cooldown_remains_sec,omitempty"`
	ConsecutiveFails int    `json:"consecutive_fails,omitempty"`
	Hits             int64  `json:"hits"`
	Failures         int64  `json:"failures"`
}

// Statuses returns a snapshot of all tokens.
func (p *Pool) Statuses() []Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	out := make([]Status, 0, len(p.slots))
	for _, s := range p.slots {
		st := Status{
			Name:             s.token.Name,
			Value:            s.token.Value,
			ConsecutiveFails: s.consecutiveFails,
			Hits:             s.hits,
			Failures:         s.failures,
		}
		if now.Before(s.cooldownEnd) {
			st.OnCooldown = true
			st.CooldownEnd = s.cooldownEnd.Unix()
			st.CooldownRemains = int(time.Until(s.cooldownEnd).Seconds())
		}
		out = append(out, st)
	}
	return out
}
