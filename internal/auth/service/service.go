// Package service — слой бизнес-логики Auth Service: регистрация, вход,
// выдача токенов доступа, просмотр профилей и начальное наполнение данными.
//
// Слой зависит только от доменной модели и от интерфейсов (портов)
// Repository, Hasher и TokenIssuer, поэтому тестируется без БД.
package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/thaperfectcell/food-delivery/internal/auth/domain"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/demo"
)

// Repository — порт хранилища пользователей.
type Repository interface {
	// Create сохраняет пользователя; domain.ErrEmailTaken, если email занят.
	Create(ctx context.Context, u *domain.User) error
	// InsertIfAbsent сохраняет пользователя, если записи с таким id/email ещё нет.
	InsertIfAbsent(ctx context.Context, u *domain.User) (bool, error)
	GetByID(ctx context.Context, id string) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	List(ctx context.Context, f domain.ListFilter) ([]*domain.User, int, error)
}

// Hasher — порт хеширования паролей (реализация — bcrypt).
type Hasher interface {
	Hash(password string) (string, error)
	Compare(hash, password string) error
}

// TokenIssuer — порт выпуска токенов доступа (реализация — pkg/auth.TokenManager).
type TokenIssuer interface {
	Issue(userID string, role auth.Role, name string) (string, time.Time, error)
}

// Service реализует сценарии использования Auth Service.
type Service struct {
	repo   Repository
	hasher Hasher
	tokens TokenIssuer
	log    *slog.Logger
	now    func() time.Time
	newID  func() string

	dummyOnce sync.Once
	dummyHash string
}

// New создаёт сервис.
func New(repo Repository, hasher Hasher, tokens TokenIssuer, log *slog.Logger) *Service {
	return &Service{
		repo:   repo,
		hasher: hasher,
		tokens: tokens,
		log:    log,
		now:    func() time.Time { return time.Now().UTC() },
		newID:  uuid.NewString,
	}
}

// RegisterInput — регистрационные данные.
type RegisterInput struct {
	Email    string
	Password string
	Name     string
	Phone    string
	Role     auth.Role
}

// AuthResult — результат регистрации или входа.
type AuthResult struct {
	User      *domain.User
	Token     string
	ExpiresAt time.Time
}

// Register создаёт учётную запись и сразу выдаёт токен доступа.
func (s *Service) Register(ctx context.Context, in RegisterInput) (*AuthResult, error) {
	if !domain.RegistrableRole(in.Role) {
		return nil, domain.ErrRoleNotAllowed
	}
	email := domain.NormalizeEmail(in.Email)
	if err := domain.ValidateEmail(email); err != nil {
		return nil, err
	}
	if err := domain.ValidatePassword(in.Password); err != nil {
		return nil, err
	}
	name, err := domain.NormalizeName(in.Name)
	if err != nil {
		return nil, err
	}
	phone, err := domain.NormalizePhone(in.Phone)
	if err != nil {
		return nil, err
	}
	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return nil, err
	}

	now := s.now()
	u := &domain.User{
		ID:           s.newID(),
		Email:        email,
		PasswordHash: hash,
		Name:         name,
		Phone:        phone,
		Role:         in.Role,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.repo.Create(ctx, u); err != nil {
		return nil, err
	}
	s.log.InfoContext(ctx, "user registered", slog.String("user_id", u.ID), slog.String("role", string(u.Role)))
	return s.issue(u)
}

// Login проверяет учётные данные и выдаёт токен доступа. Для любой ошибки
// (нет пользователя, неверный пароль) возвращается одинаковое сообщение,
// а при отсутствии пользователя всё равно выполняется сравнение хеша —
// это затрудняет перебор существующих email по времени ответа.
func (s *Service) Login(ctx context.Context, email, password string) (*AuthResult, error) {
	email = domain.NormalizeEmail(email)
	u, err := s.repo.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			_ = s.hasher.Compare(s.dummy(), password)
			return nil, domain.ErrInvalidCredentials
		}
		return nil, err
	}
	if err := s.hasher.Compare(u.PasswordHash, password); err != nil {
		s.log.InfoContext(ctx, "login failed", slog.String("user_id", u.ID))
		return nil, domain.ErrInvalidCredentials
	}
	s.log.InfoContext(ctx, "user logged in", slog.String("user_id", u.ID), slog.String("role", string(u.Role)))
	return s.issue(u)
}

// GetUser возвращает профиль: свой — любому пользователю, чужой — администратору.
func (s *Service) GetUser(ctx context.Context, actor auth.Identity, id string) (*domain.User, error) {
	if actor.IsZero() {
		return nil, domain.ErrUnauthenticated
	}
	if !actor.Privileged() && actor.UserID != id {
		return nil, domain.ErrForbidden
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, domain.ErrNotFound
	}
	return s.repo.GetByID(ctx, id)
}

// ListUsers возвращает список пользователей (только для администратора).
func (s *Service) ListUsers(ctx context.Context, actor auth.Identity, f domain.ListFilter) ([]*domain.User, int, error) {
	if !actor.Privileged() {
		return nil, 0, domain.ErrForbidden
	}
	if f.Role != "" && !f.Role.Valid() {
		return nil, 0, domain.ErrRoleNotAllowed
	}
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	return s.repo.List(ctx, f)
}

// SeedDemo идемпотентно создаёт демонстрационных пользователей (pkg/demo).
// Возвращает количество добавленных записей.
func (s *Service) SeedDemo(ctx context.Context) (int, error) {
	hash, err := s.hasher.Hash(demo.Password)
	if err != nil {
		return 0, err
	}
	now := s.now()
	added := 0
	for _, d := range demo.Users {
		u := &domain.User{
			ID:           d.ID,
			Email:        domain.NormalizeEmail(d.Email),
			PasswordHash: hash,
			Name:         d.Name,
			Phone:        d.Phone,
			Role:         auth.Role(d.Role),
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		ok, err := s.repo.InsertIfAbsent(ctx, u)
		if err != nil {
			return added, err
		}
		if ok {
			added++
		}
	}
	return added, nil
}

func (s *Service) issue(u *domain.User) (*AuthResult, error) {
	token, exp, err := s.tokens.Issue(u.ID, u.Role, u.Name)
	if err != nil {
		return nil, err
	}
	return &AuthResult{User: u, Token: token, ExpiresAt: exp}, nil
}

// dummy возвращает хеш случайного пароля для сравнения при неизвестном email.
func (s *Service) dummy() string {
	s.dummyOnce.Do(func() {
		h, err := s.hasher.Hash(uuid.NewString())
		if err == nil {
			s.dummyHash = h
		}
	})
	return s.dummyHash
}
