// Package provider — исходящий адаптер Payment Service к платёжному провайдеру.
//
// В прототипе (MVP) провайдер эмулируется: обработка занимает настраиваемое
// время (PAYMENT_DELAY), платежи сверх лимита (PAYMENT_MAX_AMOUNT) отклоняются.
// Как и реальные эквайринговые API, эмулятор идемпотентен: повторный запрос с
// тем же ключом возвращает ту же ссылку на транзакцию.
package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/thaperfectcell/food-delivery/internal/payment/domain"
	"github.com/thaperfectcell/food-delivery/internal/payment/service"
)

// Причины отказа эмулируемого провайдера.
const (
	ReasonLimitExceeded = "превышен лимит суммы платежа"
	ReasonInvalidAmount = "некорректная сумма платежа"
)

// Simulator — эмулятор платёжного провайдера (реализует service.Provider).
type Simulator struct {
	Delay     time.Duration // имитация сетевой задержки и обработки у провайдера
	MaxAmount int64         // максимальная сумма платежа, копейки
}

var _ service.Provider = (*Simulator)(nil)

// NewSimulator создаёт эмулятор.
func NewSimulator(delay time.Duration, maxAmount int64) *Simulator {
	return &Simulator{Delay: delay, MaxAmount: maxAmount}
}

// Charge «списывает» средства.
func (s *Simulator) Charge(ctx context.Context, idempotencyKey string, amount int64) (domain.ChargeResult, error) {
	if err := s.wait(ctx); err != nil {
		return domain.ChargeResult{}, err
	}
	switch {
	case amount <= 0:
		return domain.ChargeResult{Approved: false, DeclineReason: ReasonInvalidAmount}, nil
	case s.MaxAmount > 0 && amount > s.MaxAmount:
		return domain.ChargeResult{Approved: false, DeclineReason: ReasonLimitExceeded}, nil
	}
	return domain.ChargeResult{Approved: true, ProviderRef: Ref(idempotencyKey)}, nil
}

// Refund «возвращает» средства (эмуляция всегда успешна).
func (s *Simulator) Refund(ctx context.Context, _ string, _ int64) error {
	return s.wait(ctx)
}

func (s *Simulator) wait(ctx context.Context) error {
	if s.Delay <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(s.Delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Ref — детерминированная ссылка на транзакцию вида SIM-1A2B3C4D.
func Ref(idempotencyKey string) string {
	sum := sha256.Sum256([]byte(idempotencyKey))
	return "SIM-" + strings.ToUpper(hex.EncodeToString(sum[:4]))
}
