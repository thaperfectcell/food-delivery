package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/thaperfectcell/food-delivery/internal/gateway/httpx"
)

// decodeJSON разбирает JSON-тело запроса в dst. Если optional == true,
// пустое тело допустимо (dst остаётся с нулевыми значениями).
// При ошибке отправляет ответ клиенту и возвращает false.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, optional bool) bool {
	if r.Body == nil || r.Body == http.NoBody {
		if optional {
			return true
		}
		badRequest(w, r, "тело запроса не должно быть пустым")
		return false
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.Is(err, io.EOF):
			if optional {
				return true
			}
			badRequest(w, r, "тело запроса не должно быть пустым")
		case errors.As(err, &maxErr):
			httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "слишком большое тело запроса")
		default:
			var typeErr *json.UnmarshalTypeError
			if errors.As(err, &typeErr) && typeErr.Field != "" {
				badRequest(w, r, "некорректное значение поля «"+typeErr.Field+"»")
			} else {
				badRequest(w, r, "некорректный JSON в теле запроса")
			}
		}
		return false
	}
	if dec.More() {
		badRequest(w, r, "в теле запроса должен быть один JSON-объект")
		return false
	}
	return true
}

// pathID извлекает идентификатор (UUID) из пути запроса.
func pathID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	id := strings.TrimSpace(r.PathValue(name))
	if _, err := uuid.Parse(id); err != nil {
		badRequest(w, r, "некорректный идентификатор в пути запроса")
		return "", false
	}
	return id, true
}

// queryInt читает неотрицательный целочисленный параметр запроса.
func queryInt(w http.ResponseWriter, r *http.Request, name string, def int) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		badRequest(w, r, "параметр «"+name+"» должен быть неотрицательным целым числом")
		return 0, false
	}
	if n > 1_000_000 {
		n = 1_000_000
	}
	return n, true
}

// queryBool читает логический параметр запроса (true/1/yes).
func queryBool(r *http.Request, name string) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// paging читает limit и offset.
func paging(w http.ResponseWriter, r *http.Request) (limit, offset int32, ok bool) {
	l, ok := queryInt(w, r, "limit", 0)
	if !ok {
		return 0, 0, false
	}
	o, ok := queryInt(w, r, "offset", 0)
	if !ok {
		return 0, 0, false
	}
	if l > 100 {
		l = 100
	}
	return int32(l), int32(o), true
}
