package httpapi

import (
	"net/http"
	"strings"

	authv1 "github.com/thaperfectcell/food-delivery/gen/auth/v1"
	"github.com/thaperfectcell/food-delivery/internal/gateway/httpx"
	"github.com/thaperfectcell/food-delivery/internal/gateway/middleware"
)

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
	Phone    string `json:"phone"`
	Role     string `json:"role"`
}

// register — POST /auth/register.
func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decodeJSON(w, r, &req, false) {
		return
	}
	role, ok := roleFromName(req.Role)
	if !ok || role == authv1.Role_ROLE_ADMIN {
		badRequest(w, r, "допустимые роли: customer, restaurant, courier")
		return
	}
	if role == authv1.Role_ROLE_UNSPECIFIED {
		role = authv1.Role_ROLE_CUSTOMER
	}
	ctx, cancel := a.call(r)
	defer cancel()
	resp, err := a.c.Auth.Register(ctx, &authv1.RegisterRequest{
		Email:    strings.TrimSpace(req.Email),
		Password: req.Password,
		Name:     strings.TrimSpace(req.Name),
		Phone:    strings.TrimSpace(req.Phone),
		Role:     role,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toAuthResponse(resp))
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// login — POST /auth/login.
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req, false) {
		return
	}
	if strings.TrimSpace(req.Email) == "" || req.Password == "" {
		badRequest(w, r, "укажите email и пароль")
		return
	}
	ctx, cancel := a.call(r)
	defer cancel()
	resp, err := a.c.Auth.Login(ctx, &authv1.LoginRequest{Email: strings.TrimSpace(req.Email), Password: req.Password})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAuthResponse(resp))
}

// me — GET /auth/me.
func (a *API) me(w http.ResponseWriter, r *http.Request) {
	id, _ := middleware.Identity(r)
	ctx, cancel := a.call(r)
	defer cancel()
	u, err := a.c.Auth.GetUser(ctx, &authv1.GetUserRequest{Id: id.UserID})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toUser(u))
}

// adminUsers — GET /admin/users.
func (a *API) adminUsers(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := paging(w, r)
	if !ok {
		return
	}
	role, valid := roleFromName(r.URL.Query().Get("role"))
	if !valid {
		badRequest(w, r, "неизвестная роль")
		return
	}
	ctx, cancel := a.call(r)
	defer cancel()
	resp, err := a.c.Auth.ListUsers(ctx, &authv1.ListUsersRequest{Role: role, Limit: limit, Offset: offset})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	items := make([]userDTO, 0, len(resp.GetUsers()))
	for _, u := range resp.GetUsers() {
		items = append(items, toUser(u))
	}
	httpx.WriteJSON(w, http.StatusOK, newList(items, int(resp.GetTotal())))
}
