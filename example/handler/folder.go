package handler

import (
	"fmt"
	"net/http"

	swagger "github.com/avran02/swagger"
	"github.com/avran02/swagger/example/dto"
)

func NewFolderController() *FolderController {
	return &FolderController{}
}

type FolderController struct{}

func (c *FolderController) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.FolderCreateRequest
	// On a binding failure this responds 422 with a FastAPI-style JSON body.
	if !swagger.BindOr422(w, r, &req) {
		return
	}
	// The 409 is declared at registration via WithError, so it is documented
	// in the spec for this operation only.
	if req.Name == "duplicate" {
		swagger.WriteError(w, http.StatusConflict, dto.APIError{
			Code:    "already_exists",
			Message: "folder already exists",
		})
		return
	}
	_, _ = fmt.Fprintf(w, "Create folder %q\n", req.Name)
}

func (c *FolderController) Get(w http.ResponseWriter, r *http.Request) {
	_, _ = fmt.Fprintln(w, "Get folder")
}

func (c *FolderController) List(w http.ResponseWriter, r *http.Request) {
	var req dto.FolderListRequest
	if !swagger.BindOr422(w, r, &req) {
		return
	}
	_, _ = fmt.Fprintf(w, "List folders: page=%d page_size=%d\n", req.Page, req.PageSize)
}
