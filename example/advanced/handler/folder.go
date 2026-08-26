package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	swagger "github.com/avran02/swagger"
	"github.com/avran02/swagger/example/advanced/dto"
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
	_, _ = fmt.Fprintf(w, "Create folder %q (visibility=%s)\n", req.Name, req.Visibility)
}

func (c *FolderController) Get(w http.ResponseWriter, r *http.Request) {
	var req dto.FolderGetRequest
	if !swagger.BindOr422(w, r, &req) {
		return
	}
	// The 410 uses a per-endpoint schema (dto.GoneError) declared via
	// WithErrorSchema — distinct from the global Config.ErrorSchema.
	if req.ID == "archived" {
		swagger.WriteError(w, http.StatusGone, dto.GoneError{
			Code:       "archived",
			Message:    "folder was archived",
			ArchivedAt: time.Now(),
		})
		return
	}
	_, _ = fmt.Fprintf(w, "Get folder %q (expand=%s)\n", req.ID, req.Expand)
}

func (c *FolderController) List(w http.ResponseWriter, r *http.Request) {
	var req dto.FolderListRequest
	if !swagger.BindOr422(w, r, &req) {
		return
	}
	_, _ = fmt.Fprintf(w, "List folders: page=%d page_size=%d\n", req.Page, req.PageSize)
}

// Tree returns a small nested folder tree, exercising the recursive
// FolderTreeNodeResponse ($ref back to itself) with real data, not just schema.
func (c *FolderController) Tree(w http.ResponseWriter, r *http.Request) {
	var req dto.FolderTreeRequest
	if !swagger.BindOr422(w, r, &req) {
		return
	}
	resp := dto.FolderTreeResponse{
		Items: []*dto.FolderTreeNodeResponse{
			{
				ID:    1,
				Title: "Root",
				Children: []*dto.FolderTreeNodeResponse{
					{ID: 2, ParentFolderID: int64Ptr(1), Title: "Child A"},
					{ID: 3, ParentFolderID: int64Ptr(1), Title: "Child B"},
				},
			},
		},
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

func int64Ptr(v int64) *int64 { return &v }
