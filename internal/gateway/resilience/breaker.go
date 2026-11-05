// Package resilience реализует паттерн Circuit Breaker («автоматический
// выключатель») для исходящих gRPC-вызовов API Gateway.
//
// Если сервис перестал отвечать (подряд threshold ошибок Unavailable или
// DeadlineExceeded), выключатель «размыкается» (open) и в течение openTimeout
// вызовы отклоняются сразу, без ожидания таймаутов, — это защищает шлюз от
// накопления зависших запросов и даёт сервису время восстановиться. Затем
// выключатель переходит в полуоткрытое состояние (half-open) и пропускает один
// пробный вызов: успех замыкает цепь (closed), неудача снова размыкает её.
package resilience

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// State — состояние выключателя.
type State int

const (
	StateClosed   State = iota // вызовы проходят, считаются ошибки
	StateOpen                  // вызовы отклоняются
	StateHalfOpen              // пропускается один пробный вызов
)

func (s State) String() string {
	switch s {
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "closed"
	}
}

// ErrOpen возвращается вместо вызова, пока выключатель разомкнут.
var ErrOpen = status.Error(codes.Unavailable, "сервис временно недоступен")

// Breaker — выключатель для одного downstream-сервиса.
type Breaker struct {
	name        string
	threshold   int
	openTimeout time.Duration
	now         func() time.Time
	onChange    func(name string, from, to State)

	mu       sync.Mutex
	state    State
	failures int
	openedAt time.Time
	probing  bool
}

// Option настраивает выключатель.
type Option func(*Breaker)

// WithThreshold задаёт число последовательных отказов до размыкания.
func WithThreshold(n int) Option { return func(b *Breaker) { b.threshold = n } }

// WithOpenTimeout задаёт время нахождения в разомкнутом состоянии.
func WithOpenTimeout(d time.Duration) Option { return func(b *Breaker) { b.openTimeout = d } }

// WithClock подменяет часы (для тестов).
func WithClock(now func() time.Time) Option { return func(b *Breaker) { b.now = now } }

// WithStateChange задаёт обработчик смены состояния (например, для журнала).
func WithStateChange(fn func(name string, from, to State)) Option {
	return func(b *Breaker) { b.onChange = fn }
}

// New создаёт выключатель: по умолчанию 5 отказов подряд, пауза 10 секунд.
func New(name string, opts ...Option) *Breaker {
	b := &Breaker{name: name, threshold: 5, openTimeout: 10 * time.Second, now: time.Now}
	for _, o := range opts {
		o(b)
	}
	return b
}

// Name — имя защищаемого сервиса.
func (b *Breaker) Name() string { return b.name }

// State возвращает текущее состояние (с учётом истечения паузы).
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == StateOpen && b.now().Sub(b.openedAt) >= b.openTimeout {
		return StateHalfOpen
	}
	return b.state
}

// Allow решает, можно ли выполнить вызов. При успехе вызывающий обязан
// сообщить результат через Done.
func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case StateClosed:
		return nil
	case StateOpen:
		if b.now().Sub(b.openedAt) < b.openTimeout {
			return ErrOpen
		}
		b.setState(StateHalfOpen)
		b.probing = true
		return nil
	default: // half-open: допускается только один пробный вызов
		if b.probing {
			return ErrOpen
		}
		b.probing = true
		return nil
	}
}

// Done фиксирует результат вызова, разрешённого Allow.
func (b *Breaker) Done(err error) {
	failed := IsFailure(err)
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case StateHalfOpen:
		b.probing = false
		if failed {
			b.open()
		} else {
			b.failures = 0
			b.setState(StateClosed)
		}
	case StateClosed:
		if !failed {
			b.failures = 0
			return
		}
		b.failures++
		if b.failures >= b.threshold {
			b.open()
		}
	}
}

func (b *Breaker) open() {
	b.openedAt = b.now()
	b.failures = 0
	b.setState(StateOpen)
}

func (b *Breaker) setState(s State) {
	if b.state == s {
		return
	}
	from := b.state
	b.state = s
	if b.onChange != nil {
		// Обработчик вызывается без удержания блокировки, чтобы он мог
		// безопасно обращаться к выключателю.
		go b.onChange(b.name, from, s)
	}
}

// IsFailure — считается ли ошибка признаком недоступности сервиса.
// Бизнес-ошибки (NotFound, InvalidArgument и т.п.) означают, что сервис
// работает, и не влияют на состояние выключателя.
func IsFailure(err error) bool {
	if err == nil {
		return false
	}
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	}
	return false
}

// UnaryClientInterceptor возвращает клиентский перехватчик gRPC.
func (b *Breaker) UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if err := b.Allow(); err != nil {
			return err
		}
		err := invoker(ctx, method, req, reply, cc, opts...)
		// Отмена запроса самим клиентом (например, пользователь закрыл
		// страницу) не говорит о недоступности сервиса.
		if ctx.Err() == context.Canceled {
			b.Done(nil)
			return err
		}
		b.Done(err)
		return err
	}
}
