// Package repository — слой доступа к данным Auth Service (PostgreSQL, pgx).
package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thaperfectcell/food-delivery/internal/auth/domain"
	"github.com/thaperfectcell/food-delivery/internal/auth/service"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/postgres"
)

// Repository реализует service.Repository.
type Repository struct {
	pool *pgxpool.Pool
}

var _ service.Repository = (*Repository)(nil)

// New создаёт репозиторий.
func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

const userColumns = `id, email, password_hash, name, phone, role, created_at, updated_at`

// Create сохраняет нового пользователя.
func (r *Repository) Create(ctx context.Context, u *domain.User) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, phone, role, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		u.ID, u.Email, u.PasswordHash, u.Name, u.Phone, string(u.Role), u.CreatedAt, u.UpdatedAt)
	if err != nil {
		if postgres.IsUniqueViolation(err) {
			return domain.ErrEmailTaken
		}
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

// InsertIfAbsent сохраняет пользователя, если нет записи с тем же id или email
// (используется для идемпотентного наполнения демонстрационными данными).
func (r *Repository) InsertIfAbsent(ctx context.Context, u *domain.User) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, phone, role, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT DO NOTHING`,
		u.ID, u.Email, u.PasswordHash, u.Name, u.Phone, string(u.Role), u.CreatedAt, u.UpdatedAt)
	if err != nil {
		return false, fmt.Errorf("seed user: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// GetByID возвращает пользователя по идентификатору.
func (r *Repository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	return scanUser(r.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
}

// GetByEmail возвращает пользователя по (нормализованному) email.
func (r *Repository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	return scanUser(r.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE email = $1`, email))
}

// List возвращает страницу пользователей и общее количество.
func (r *Repository) List(ctx context.Context, f domain.ListFilter) ([]*domain.User, int, error) {
	cond, args := "", []any{}
	if f.Role != "" {
		cond = " WHERE role = $1"
		args = append(args, string(f.Role))
	}

	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM users`+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}

	args = append(args, f.Limit, f.Offset)
	query := fmt.Sprintf(`SELECT %s FROM users%s ORDER BY created_at, email LIMIT $%d OFFSET $%d`,
		userColumns, cond, len(args)-1, len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var users []*domain.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

func scanUser(row pgx.Row) (*domain.User, error) {
	var (
		u    domain.User
		role string
	)
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.Phone, &role, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if postgres.IsNoRows(err) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scan user: %w", err)
	}
	u.Role = auth.Role(role)
	return &u, nil
}
