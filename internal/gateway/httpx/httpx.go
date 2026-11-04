// Package httpx — общие помощники HTTP-слоя шлюза: JSON-ответы и единый
// формат ошибок {"error":{"code":"…","message":"…"},"request_id":"…"}.
package httpx

import (
	"encoding/json"
	"net/http"

	"github.com/thaperfectcell/food-delivery/pkg/requestid"
)

// ErrorInfo — описание ошибки.
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ErrorBody — тело ответа с ошибкой.
type ErrorBody struct {
	Error     ErrorInfo `json:"error"`
	RequestID string    `json:"request_id,omitempty"`
}

// WriteJSON сериализует v в JSON и отправляет с указанным статусом.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// WriteError отправляет ошибку в едином формате.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	WriteJSON(w, status, ErrorBody{
		Error:     ErrorInfo{Code: code, Message: message},
		RequestID: requestid.From(r.Context()),
	})
}
