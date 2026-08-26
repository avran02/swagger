package handler

import (
	"fmt"
	"net/http"
	"time"

	swagger "github.com/avran02/swagger"
	"github.com/avran02/swagger/example/advanced/dto"
)

func NewUserController() *UserController {
	return &UserController{}
}

type UserController struct{}

// Create binds from every source BindRequest supports in one call: JSON body
// (name/age/role), query (ref), path (id), header (X-Request-Token) and
// cookie (session).
func (c *UserController) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateUserRequest
	if !swagger.BindOr422(w, r, &req) {
		return
	}
	resp := dto.CreateUserResponse{
		ID:        req.ID,
		Name:      req.Name,
		Role:      req.Role,
		CreatedAt: time.Now(),
	}
	_, _ = fmt.Fprintf(w, "Create user %q role=%s session=%s ref=%s token=%s -> %+v\n",
		resp.Name, resp.Role, req.SessionID, req.Ref, req.Token, resp)
}
