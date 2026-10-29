// Package security — инфраструктурные адаптеры безопасности Auth Service.
package security

import (
	"errors"

	"golang.org/x/crypto/bcrypt"

	"github.com/thaperfectcell/food-delivery/internal/auth/service"
)

// BcryptHasher хеширует пароли алгоритмом bcrypt (адаптивная функция с солью).
// Реализует service.Hasher.
type BcryptHasher struct {
	Cost int // 0 — bcrypt.DefaultCost
}

var _ service.Hasher = BcryptHasher{}

// Hash возвращает bcrypt-хеш пароля.
func (h BcryptHasher) Hash(password string) (string, error) {
	cost := h.Cost
	if cost == 0 {
		cost = bcrypt.DefaultCost
	}
	b, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Compare сравнивает пароль с хешем (nil — совпадает).
func (h BcryptHasher) Compare(hash, password string) error {
	if hash == "" {
		return errors.New("empty password hash")
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}
