package main

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	antiban "github.com/TA-rathnayaka/whatsmeow-antiban"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

var (
	gatesMu sync.Mutex
	gates   = map[string]*botGate{}
)

var errAntibanBlocked = errors.New("blocked by antiban")

type botGate struct {
	health      *antiban.Health
	banRecovery *antiban.BanRecovery
	timelock    *antiban.Timelock
	rateLimiter *antiban.RateLimiter

	mu         sync.Mutex
	knownChats map[string]struct{}
	rng        *rand.Rand
}

func newBotGate() *botGate {
	now := time.Now()
	preset := antiban.Resolve("moderate")

	return &botGate{
		health:      antiban.NewHealth(preset.AutoPauseAt),
		banRecovery: antiban.NewBanRecovery(),
		timelock:    antiban.NewTimelock(),
		rateLimiter: antiban.NewRateLimiter(preset, now.UnixNano()),
		knownChats:  make(map[string]struct{}),
		rng:         rand.New(rand.NewSource(now.UnixNano())),
	}
}

func gateFor(txtid string) *botGate {
	gatesMu.Lock()
	defer gatesMu.Unlock()

	if gate, ok := gates[txtid]; ok {
		return gate
	}

	gate := newBotGate()
	gates[txtid] = gate

	return gate
}

func (g *botGate) isKnownChat(jid string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	_, ok := g.knownChats[jid]
	return ok
}

func (g *botGate) markKnownChat(jid string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.knownChats[jid] = struct{}{}
}

func (g *botGate) typingPlan(content string) []antiban.TypingStep {
	g.mu.Lock()
	defer g.mu.Unlock()

	return antiban.ComputeTypingPlan(
		g.rng,
		len([]rune(content)),
		0.25,
	)
}

func (g *botGate) beforeSend(recipient types.JID, content string, now time.Time) (time.Duration, error) {
	isGroup := recipient.Server == types.GroupServer
	isKnown := g.isKnownChat(recipient.String())

	if g.health.Blocked(now) {
		return 0, errAntibanBlocked
	}

	if g.banRecovery.Blocked(now) {
		return 0, errAntibanBlocked
	}

	if !g.timelock.CanSend(isGroup, isKnown, now) {
		return 0, errAntibanBlocked
	}

	// Empty content hash intentionally disables identical-message blocking.
	delayMs := g.rateLimiter.GetDelay(
		recipient.String(),
		"",
		!isKnown,
		len([]rune(content)),
		now,
	)
	if delayMs < 0 {
		return 0, errAntibanBlocked
	}

	if multiplier := g.banRecovery.Multiplier(now); multiplier > 0 && multiplier < 1 {
		delayMs = int(float64(delayMs) / multiplier)
	}

	return time.Duration(delayMs) * time.Millisecond, nil
}

func (g *botGate) afterSend(recipient types.JID, now time.Time) {
	g.rateLimiter.Record(recipient.String(), "", now)
	g.markKnownChat(recipient.String())
}

func (g *botGate) afterSendFailed(now time.Time) {
	g.health.RecordFailedMessage(now)
}

func withAntiban(
	ctx context.Context,
	client *whatsmeow.Client,
	txtid string,
	recipient types.JID,
	content string,
	send func() error,
) error {
	gate := gateFor(txtid)

	delay, err := gate.beforeSend(recipient, content, time.Now())
	if err != nil {
		return err
	}

	if delay > 0 {
		timer := time.NewTimer(delay)

		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	plan := gate.typingPlan(content)

	err = antiban.ExecuteTypingPlan(ctx, plan, func(state string) error {
		return client.SendChatPresence(
			context.Background(),
			recipient,
			types.ChatPresence(state),
			types.ChatPresenceMedia(""),
		)
	})
	if err != nil {
		return err
	}

	if err := send(); err != nil {
		gate.afterSendFailed(time.Now())
		return err
	}

	gate.afterSend(recipient, time.Now())

	return nil
}