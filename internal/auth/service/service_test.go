package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thaperfectcell/food-delivery/internal/auth/domain"
	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/demo"
	"github.com/thaperfectcell/food-delivery/pkg/logger"
)

// ------------------------------------------------------------ тестовые двойники

type memRepo struct {
	mu    sync.Mutex
	users map[string]*domain.User
}

func newMemRepo() *memRepo { return &memRepo{users: map[string]*domain.User{}} }

func (r *memRepo) Create(_ context.Context, u *domain.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.users {
		if x.Email == u.Email {
			return domain.ErrEmailTaken
		}
	}
	c := *u
	r.users[u.ID] = &c
	return nil
}

func (r *memRepo) InsertIfAbsent(_ context.Context, u *domain.User) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.users {
		if x.ID == u.ID || x.Email == u.Email {
			return false, nil
		}
	}
	c := *u
	r.users[u.ID] = &c
	return true, nil
}

func (r *memRepo) GetByID(_ context.Context, id string) (*domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	c := *u
	return &c, nil
}

func (r *memRepo) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.users {
		if u.Email == email {
			c := *u
			return &c, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *memRepo) List(_ context.Context, f domain.ListFilter) ([]*domain.User, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var all []*domain.User
	for _, u := range r.users {
		if f.Role != "" && u.Role != f.Role {
			continue
		}
		c := *u
		all = append(all, &c)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Email < all[j].Email })
	total := len(all)
	if f.Offset >= len(all) {
		return nil, total, nil
	}
	all = all[f.Offset:]
	if len(all) > f.Limit {
		all = all[:f.Limit]
	}
	return all, total, nil
}

// fakeHasher — быстрый детерминированный «хеш» для unit-тестов.
type fakeHasher struct{}

func (fakeHasher) Hash(p string) (string, error) { return "hashed:" + p, nil }

func (fakeHasher) Compare(hash, p string) error {
	if hash != "hashed:"+p {
		return errors.New("mismatch")
	}
	return nil
}

func newService() (*Service, *memRepo, *auth.TokenManager) {
	repo := newMemRepo()
	tokens := auth.NewTokenManager("test-secret", time.Hour)
	return New(repo, fakeHasher{}, tokens, logger.Discard()), repo, tokens
}

func validInput() RegisterInput {
	return RegisterInput{
		Email:    "  Client@Example.COM ",
		Password: "password123",
		Name:     "  Алексей ",
		Phone:    "+7 900 000-00-00",
		Role:     auth.RoleCustomer,
	}
}

// ------------------------------------------------------------ тесты

func TestRegisterIssuesToken(t *testing.T) {
	svc, repo, tokens := newService()
	res, err := svc.Register(context.Background(), validInput())
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if res.User.Email != "client@example.com" || res.User.Name != "Алексей" || res.User.Role != auth.RoleCustomer {
		t.Fatalf("unexpected user: %+v", res.User)
	}
	stored, _ := repo.GetByID(context.Background(), res.User.ID)
	if stored == nil || stored.PasswordHash == "password123" || stored.PasswordHash == "" {
		t.Fatal("password must be stored hashed")
	}
	id, err := tokens.Parse(res.Token)
	if err != nil {
		t.Fatalf("issued token must be valid: %v", err)
	}
	if id.UserID != res.User.ID || id.Role != auth.RoleCustomer || id.Name != "Алексей" {
		t.Fatalf("unexpected token identity: %+v", id)
	}
	if !res.ExpiresAt.After(time.Now()) {
		t.Fatal("token expiration must be in the future")
	}
}

func TestRegisterValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*RegisterInput)
	}{
		{"bad email", func(in *RegisterInput) { in.Email = "client.example.com" }},
		{"short password", func(in *RegisterInput) { in.Password = "short" }},
		{"empty name", func(in *RegisterInput) { in.Name = "   " }},
		{"long name", func(in *RegisterInput) { in.Name = strings.Repeat("я", 101) }},
		{"admin role", func(in *RegisterInput) { in.Role = auth.RoleAdmin }},
		{"system role", func(in *RegisterInput) { in.Role = auth.RoleSystem }},
		{"unknown role", func(in *RegisterInput) { in.Role = "boss" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := newService()
			in := validInput()
			tc.mutate(&in)
			_, err := svc.Register(context.Background(), in)
			if !apperr.Is(err, apperr.KindInvalid) {
				t.Fatalf("expected invalid argument, got %v", err)
			}
		})
	}
}

func TestRegisterAllowsBusinessRoles(t *testing.T) {
	svc, _, _ := newService()
	for i, role := range []auth.Role{auth.RoleCustomer, auth.RoleRestaurant, auth.RoleCourier} {
		in := validInput()
		in.Email = strings.Repeat("x", i+1) + "@example.com"
		in.Role = role
		if _, err := svc.Register(context.Background(), in); err != nil {
			t.Fatalf("role %s must be allowed: %v", role, err)
		}
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	svc, _, _ := newService()
	if _, err := svc.Register(context.Background(), validInput()); err != nil {
		t.Fatal(err)
	}
	in := validInput()
	in.Email = "CLIENT@example.com" // тот же адрес в другом регистре
	_, err := svc.Register(context.Background(), in)
	if !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if apperr.MessageOf(err) != "пользователь с таким email уже существует" {
		t.Fatalf("unexpected message %q", apperr.MessageOf(err))
	}
}

func TestLogin(t *testing.T) {
	ctx := context.Background()
	svc, _, tokens := newService()
	reg, err := svc.Register(ctx, validInput())
	if err != nil {
		t.Fatal(err)
	}

	res, err := svc.Login(ctx, " CLIENT@example.com ", "password123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res.User.ID != reg.User.ID {
		t.Fatal("login must return the registered user")
	}
	if _, err := tokens.Parse(res.Token); err != nil {
		t.Fatalf("token must be valid: %v", err)
	}

	_, errWrong := svc.Login(ctx, "client@example.com", "wrong-password")
	_, errUnknown := svc.Login(ctx, "nobody@example.com", "password123")
	for _, err := range []error{errWrong, errUnknown} {
		if !apperr.Is(err, apperr.KindUnauthenticated) {
			t.Fatalf("expected unauthenticated, got %v", err)
		}
		if apperr.MessageOf(err) != "неверный email или пароль" {
			t.Fatalf("message must not reveal the reason: %q", apperr.MessageOf(err))
		}
	}
}

func TestGetUserAccess(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService()
	reg, _ := svc.Register(ctx, validInput())
	self := auth.Identity{UserID: reg.User.ID, Role: auth.RoleCustomer}
	other := auth.Identity{UserID: uuid.NewString(), Role: auth.RoleCustomer}
	admin := auth.Identity{UserID: uuid.NewString(), Role: auth.RoleAdmin}

	if u, err := svc.GetUser(ctx, self, reg.User.ID); err != nil || u.ID != reg.User.ID {
		t.Fatalf("user must see own profile: %v", err)
	}
	if _, err := svc.GetUser(ctx, other, reg.User.ID); !apperr.Is(err, apperr.KindForbidden) {
		t.Fatalf("other user must be forbidden, got %v", err)
	}
	if _, err := svc.GetUser(ctx, admin, reg.User.ID); err != nil {
		t.Fatalf("admin must see any profile: %v", err)
	}
	if _, err := svc.GetUser(ctx, admin, uuid.NewString()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown user must be not found, got %v", err)
	}
	if _, err := svc.GetUser(ctx, auth.Identity{}, reg.User.ID); !apperr.Is(err, apperr.KindUnauthenticated) {
		t.Fatalf("anonymous call must be unauthenticated, got %v", err)
	}
}

func TestListUsersAdminOnly(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService()
	for i, role := range []auth.Role{auth.RoleCustomer, auth.RoleCourier, auth.RoleCourier} {
		in := validInput()
		in.Email = strings.Repeat("u", i+1) + "@example.com"
		in.Role = role
		if _, err := svc.Register(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	customer := auth.Identity{UserID: uuid.NewString(), Role: auth.RoleCustomer}
	if _, _, err := svc.ListUsers(ctx, customer, domain.ListFilter{}); !apperr.Is(err, apperr.KindForbidden) {
		t.Fatalf("customer must not list users, got %v", err)
	}
	admin := auth.Identity{UserID: uuid.NewString(), Role: auth.RoleAdmin}
	all, total, err := svc.ListUsers(ctx, admin, domain.ListFilter{})
	if err != nil || total != 3 || len(all) != 3 {
		t.Fatalf("admin must see all users: total=%d len=%d err=%v", total, len(all), err)
	}
	couriers, total, err := svc.ListUsers(ctx, admin, domain.ListFilter{Role: auth.RoleCourier})
	if err != nil || total != 2 || len(couriers) != 2 {
		t.Fatalf("role filter: total=%d len=%d err=%v", total, len(couriers), err)
	}
	page, total, _ := svc.ListUsers(ctx, admin, domain.ListFilter{Limit: 1, Offset: 1})
	if total != 3 || len(page) != 1 {
		t.Fatalf("pagination: total=%d len=%d", total, len(page))
	}
}

func TestSeedDemoIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService()
	added, err := svc.SeedDemo(ctx)
	if err != nil || added != len(demo.Users) {
		t.Fatalf("first seed: added=%d err=%v", added, err)
	}
	added, err = svc.SeedDemo(ctx)
	if err != nil || added != 0 {
		t.Fatalf("second seed must add nothing: added=%d err=%v", added, err)
	}
	res, err := svc.Login(ctx, "courier@foodflow.local", demo.Password)
	if err != nil || res.User.ID != demo.Courier1ID || res.User.Role != auth.RoleCourier {
		t.Fatalf("demo courier must be able to log in: %v", err)
	}
	admin, err := svc.Login(ctx, "admin@foodflow.local", demo.Password)
	if err != nil || admin.User.Role != auth.RoleAdmin {
		t.Fatalf("demo admin must be able to log in: %v", err)
	}
}
