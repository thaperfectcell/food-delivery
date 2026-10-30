// Package domain содержит доменную модель Restaurant Service: рестораны,
// позиции меню, правила их проверки и доменные ошибки.
package domain

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/thaperfectcell/food-delivery/pkg/apperr"
)

// Ограничения полей.
const (
	MaxNameLength        = 100
	MaxDescriptionLength = 1000
	MaxAddressLength     = 300
	MaxCuisineLength     = 50
	MaxCategoryLength    = 50
	MinPrice             = 1          // 1 копейка
	MaxPrice             = 10_000_000 // 100 000 ₽
)

// Restaurant — ресторан-партнёр.
type Restaurant struct {
	ID          string
	OwnerID     string
	Name        string
	Description string
	Address     string
	Cuisine     string
	IsActive    bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// MenuItem — позиция меню (цена в копейках).
type MenuItem struct {
	ID           string
	RestaurantID string
	Name         string
	Description  string
	Category     string
	Price        int64
	IsAvailable  bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// RestaurantInput — данные для создания ресторана.
type RestaurantInput struct {
	Name        string
	Description string
	Address     string
	Cuisine     string
}

// RestaurantPatch — частичное изменение ресторана (nil — поле не меняется).
type RestaurantPatch struct {
	Name        *string
	Description *string
	Address     *string
	Cuisine     *string
	IsActive    *bool
}

// MenuItemInput — данные для создания позиции меню.
type MenuItemInput struct {
	Name        string
	Description string
	Category    string
	Price       int64
}

// MenuItemPatch — частичное изменение позиции меню.
type MenuItemPatch struct {
	Name        *string
	Description *string
	Category    *string
	Price       *int64
	IsAvailable *bool
}

// ListFilter — параметры выборки ресторанов.
type ListFilter struct {
	OwnerID         string
	IncludeInactive bool
	Cuisine         string
	Query           string
	Limit           int
	Offset          int
}

// Ошибки доменного уровня.
var (
	ErrRestaurantNotFound = apperr.NotFound("ресторан не найден")
	ErrMenuItemNotFound   = apperr.NotFound("позиция меню не найдена")
	ErrForbidden          = apperr.Forbidden("нет прав на управление этим рестораном")
	ErrNameRequired       = apperr.Invalid("не указано название")
	ErrNameTooLong        = apperr.Invalid("название слишком длинное (не более 100 символов)")
	ErrAddressRequired    = apperr.Invalid("не указан адрес")
	ErrAddressTooLong     = apperr.Invalid("адрес слишком длинный (не более 300 символов)")
	ErrDescriptionTooLong = apperr.Invalid("описание слишком длинное (не более 1000 символов)")
	ErrCuisineTooLong     = apperr.Invalid("название кухни слишком длинное")
	ErrCategoryTooLong    = apperr.Invalid("название категории слишком длинное")
	ErrInvalidPrice       = apperr.Invalid("цена должна быть от 0,01 до 100 000 ₽")
)

// NewRestaurant создаёт ресторан с проверкой полей. Новый ресторан активен.
func NewRestaurant(id, ownerID string, in RestaurantInput, now time.Time) (*Restaurant, error) {
	r := &Restaurant{
		ID:        id,
		OwnerID:   ownerID,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	var err error
	if r.Name, err = cleanRequired(in.Name, MaxNameLength, ErrNameRequired, ErrNameTooLong); err != nil {
		return nil, err
	}
	if r.Address, err = cleanRequired(in.Address, MaxAddressLength, ErrAddressRequired, ErrAddressTooLong); err != nil {
		return nil, err
	}
	if r.Description, err = cleanOptional(in.Description, MaxDescriptionLength, ErrDescriptionTooLong); err != nil {
		return nil, err
	}
	if r.Cuisine, err = cleanOptional(in.Cuisine, MaxCuisineLength, ErrCuisineTooLong); err != nil {
		return nil, err
	}
	return r, nil
}

// Apply применяет частичное изменение. При ошибке ресторан не меняется.
func (r *Restaurant) Apply(p RestaurantPatch, now time.Time) error {
	next := *r
	var err error
	if p.Name != nil {
		if next.Name, err = cleanRequired(*p.Name, MaxNameLength, ErrNameRequired, ErrNameTooLong); err != nil {
			return err
		}
	}
	if p.Address != nil {
		if next.Address, err = cleanRequired(*p.Address, MaxAddressLength, ErrAddressRequired, ErrAddressTooLong); err != nil {
			return err
		}
	}
	if p.Description != nil {
		if next.Description, err = cleanOptional(*p.Description, MaxDescriptionLength, ErrDescriptionTooLong); err != nil {
			return err
		}
	}
	if p.Cuisine != nil {
		if next.Cuisine, err = cleanOptional(*p.Cuisine, MaxCuisineLength, ErrCuisineTooLong); err != nil {
			return err
		}
	}
	if p.IsActive != nil {
		next.IsActive = *p.IsActive
	}
	next.UpdatedAt = now
	*r = next
	return nil
}

// NewMenuItem создаёт позицию меню с проверкой полей. Новая позиция доступна.
func NewMenuItem(id, restaurantID string, in MenuItemInput, now time.Time) (*MenuItem, error) {
	m := &MenuItem{
		ID:           id,
		RestaurantID: restaurantID,
		IsAvailable:  true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	var err error
	if m.Name, err = cleanRequired(in.Name, MaxNameLength, ErrNameRequired, ErrNameTooLong); err != nil {
		return nil, err
	}
	if m.Description, err = cleanOptional(in.Description, MaxDescriptionLength, ErrDescriptionTooLong); err != nil {
		return nil, err
	}
	if m.Category, err = cleanOptional(in.Category, MaxCategoryLength, ErrCategoryTooLong); err != nil {
		return nil, err
	}
	if err := ValidatePrice(in.Price); err != nil {
		return nil, err
	}
	m.Price = in.Price
	return m, nil
}

// Apply применяет частичное изменение позиции меню.
func (m *MenuItem) Apply(p MenuItemPatch, now time.Time) error {
	next := *m
	var err error
	if p.Name != nil {
		if next.Name, err = cleanRequired(*p.Name, MaxNameLength, ErrNameRequired, ErrNameTooLong); err != nil {
			return err
		}
	}
	if p.Description != nil {
		if next.Description, err = cleanOptional(*p.Description, MaxDescriptionLength, ErrDescriptionTooLong); err != nil {
			return err
		}
	}
	if p.Category != nil {
		if next.Category, err = cleanOptional(*p.Category, MaxCategoryLength, ErrCategoryTooLong); err != nil {
			return err
		}
	}
	if p.Price != nil {
		if err := ValidatePrice(*p.Price); err != nil {
			return err
		}
		next.Price = *p.Price
	}
	if p.IsAvailable != nil {
		next.IsAvailable = *p.IsAvailable
	}
	next.UpdatedAt = now
	*m = next
	return nil
}

// ValidatePrice проверяет допустимый диапазон цены.
func ValidatePrice(price int64) error {
	if price < MinPrice || price > MaxPrice {
		return ErrInvalidPrice
	}
	return nil
}

func cleanRequired(s string, max int, errEmpty, errLong error) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errEmpty
	}
	if utf8.RuneCountInString(s) > max {
		return "", errLong
	}
	return s, nil
}

func cleanOptional(s string, max int, errLong error) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		return "", errLong
	}
	return s, nil
}
