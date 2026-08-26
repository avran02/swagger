// Package dto holds the request/response types for the advanced example.
// Every binding source, metadata tag and schema-generation rule the library
// supports is exercised by at least one type here — see README.md for the
// full tag reference.
package dto

import "time"

// FolderCreateRequest is the request DTO for creating a folder.
type FolderCreateRequest struct {
	Name        string  `json:"name"                  example:"My Folder"`
	Description *string `json:"description,omitempty" example:"Optional description"`
	ParentID    *string `json:"parent_id,omitempty"`
	Visibility  string  `json:"visibility"             enum:"private,shared,public" example:"private" description:"Who can see this folder"`
	Ref         string  `query:"ref"                  example:"campaign-123"`
}

// FolderCreateResponse is the response DTO returned after folder creation.
type FolderCreateResponse struct {
	ID          string    `json:"id"          example:"fld_01J3K"`
	Name        string    `json:"name"        example:"My Folder"`
	Description *string   `json:"description,omitempty"`
	ParentID    *string   `json:"parent_id,omitempty"`
	Visibility  string    `json:"visibility"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// FolderGetRequest carries path and query params for fetching a folder.
type FolderGetRequest struct {
	ID     string `path:"id"      example:"fld_01J3K"`
	Expand string `query:"expand" example:"children" required:"false"`
}

// FolderGetResponse is the response DTO for a single folder.
type FolderGetResponse = FolderCreateResponse

// FolderListRequest carries pagination params.
type FolderListRequest struct {
	Page     int    `query:"page"      example:"1"  default:"1"  required:"false"`
	PageSize int    `query:"page_size" example:"20" default:"20" required:"false"`
	ParentID string `query:"parent_id"                            required:"false"`
}

// FolderListResponse wraps a list of folders.
type FolderListResponse struct {
	Items      []FolderCreateResponse `json:"items"`
	TotalCount int                    `json:"total_count" example:"42"`
	Page       int                    `json:"page"        example:"1"`
	PageSize   int                    `json:"page_size"   example:"20"`
}

// FolderTreeRequest demonstrates path + header + query in one struct, plus
// required:"false", default and description tags.
type FolderTreeRequest struct {
	ID        string `path:"id"              example:"fld_01J3K"`
	CompanyID int64  `header:"X-Company-ID"  required:"false" default:"0" description:"Company scope. Defaults to 0 when the header is absent"`
	Depth     int    `query:"depth"          required:"false" default:"4" description:"Maximum depth of the tree. 0 = maximum=32"`
}

// FolderTreeNodeResponse is a single node in the folder tree — self-referential.
type FolderTreeNodeResponse struct {
	ID             int64                     `json:"id"`
	ParentFolderID *int64                    `json:"parent_folder_id"`
	Title          string                    `json:"title"`
	Children       []*FolderTreeNodeResponse `json:"children"` // recursive, generates $ref
}

// FolderTreeResponse is the top-level tree response.
type FolderTreeResponse struct {
	Items []*FolderTreeNodeResponse `json:"items"`
}

// APIError is the error body every endpoint returns for 4xx/5xx responses.
// Wire it into the spec via swagger.Config{ErrorSchema: dto.APIError{}}.
type APIError struct {
	Code    string `json:"code"    example:"not_found"`
	Message string `json:"message" example:"folder not found"`
}

// GoneError is a per-endpoint error schema, distinct from the global
// APIError — see Registration.WithErrorSchema in main.go.
type GoneError struct {
	Code       string    `json:"code"        example:"archived"`
	Message    string    `json:"message"     example:"folder was archived"`
	ArchivedAt time.Time `json:"archived_at"`
}

// CreateUserRequest hits every binding source in a single struct: JSON body,
// query, path, header and cookie — plus the enum/format/description tags.
type CreateUserRequest struct {
	Name      string `json:"name"              example:"Alice"`
	Age       int    `json:"age"               example:"30"`
	Role      string `json:"role"              enum:"admin,member,guest" example:"member" description:"Account role"`
	Ref       string `query:"ref"              required:"false"          description:"Marketing attribution tag"`
	ID        string `path:"id"                format:"uuid"             example:"3fa85f64-5717-4562-b3fc-2c963f66afa6"`
	Token     string `header:"X-Request-Token" required:"false"`
	SessionID string `cookie:"session"         required:"false"          description:"Existing session to attach the user to"`
}

// CreateUserResponse is returned after a user is created.
type CreateUserResponse struct {
	ID        string    `json:"id"         example:"3fa85f64-5717-4562-b3fc-2c963f66afa6"`
	Name      string    `json:"name"       example:"Alice"`
	Role      string    `json:"role"       example:"member"`
	CreatedAt time.Time `json:"created_at"`
}
