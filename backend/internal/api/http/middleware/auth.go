package middleware

import (
	"encoding/json"
	"net/http"
	"strings"

	"uber-test/backend/internal/auth"
	"uber-test/backend/internal/model"
)

type Authenticator struct {
	tokens *auth.TokenManager
}

func NewAuthenticator(tokens *auth.TokenManager) *Authenticator {
	return &Authenticator{tokens: tokens}
}

func (a *Authenticator) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenString := extractBearerToken(r.Header.Get("Authorization"))
		if tokenString == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "missing authorization header"})
			return
		}

		principal, err := a.tokens.Parse(tokenString)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": err.Error()})
			return
		}

		next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
	})
}

func RequireRoles(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := auth.PrincipalFromContext(r.Context())
			if !ok {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": auth.ErrUnauthorized.Error()})
				return
			}
			if _, exists := allowed[principal.Role]; !exists {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": auth.ErrForbidden.Error()})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func CurrentPrincipal(r *http.Request) (auth.Principal, error) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		return auth.Principal{}, auth.ErrUnauthorized
	}
	return principal, nil
}

func MustBeSelfOrAdmin(r *http.Request, driverID string) error {
	principal, err := CurrentPrincipal(r)
	if err != nil {
		return err
	}
	if principal.Role == model.RoleAdmin {
		return nil
	}
	if principal.Role == model.RoleDriver && principal.DriverID == driverID {
		return nil
	}
	return auth.ErrForbidden
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func extractBearerToken(header string) string {
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
