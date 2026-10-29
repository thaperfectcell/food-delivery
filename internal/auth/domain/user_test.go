package domain

import (
	"strings"
	"testing"

	"github.com/thaperfectcell/food-delivery/pkg/auth"
)

func TestValidateEmail(t *testing.T) {
	valid := []string{"user@example.com", "a.b-c@mail.food.ru", "client@foodflow.local"}
	for _, e := range valid {
		if err := ValidateEmail(e); err != nil {
			t.Errorf("%q must be valid: %v", e, err)
		}
	}
	invalid := []string{"", "user", "@example.com", "user@", "user@example", "user@example.", "a@b@c.ru", "us er@example.com"}
	for _, e := range invalid {
		if err := ValidateEmail(e); err == nil {
			t.Errorf("%q must be invalid", e)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got := NormalizeEmail("  User@Example.COM "); got != "user@example.com" {
		t.Fatalf("got %q", got)
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("1234567"); err == nil {
		t.Error("7 characters must be rejected")
	}
	if err := ValidatePassword("пароль12"); err != nil {
		t.Errorf("8 unicode characters must be accepted: %v", err)
	}
	if err := ValidatePassword(strings.Repeat("a", 73)); err == nil {
		t.Error("password longer than 72 bytes must be rejected")
	}
}

func TestNormalizeName(t *testing.T) {
	if n, err := NormalizeName("  Иван  "); err != nil || n != "Иван" {
		t.Fatalf("got %q, %v", n, err)
	}
	if _, err := NormalizeName("   "); err == nil {
		t.Error("empty name must be rejected")
	}
	if _, err := NormalizeName(strings.Repeat("я", 101)); err == nil {
		t.Error("name longer than 100 runes must be rejected")
	}
	if _, err := NormalizeName(strings.Repeat("я", 100)); err != nil {
		t.Errorf("name of 100 runes must be accepted: %v", err)
	}
}

func TestRegistrableRole(t *testing.T) {
	for _, r := range []auth.Role{auth.RoleCustomer, auth.RoleRestaurant, auth.RoleCourier} {
		if !RegistrableRole(r) {
			t.Errorf("%s must be registrable", r)
		}
	}
	for _, r := range []auth.Role{auth.RoleAdmin, auth.RoleSystem, "", "boss"} {
		if RegistrableRole(r) {
			t.Errorf("%q must not be registrable", r)
		}
	}
}
