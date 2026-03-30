package dto

import "time"

// FolderCreateRequest is the request DTO for creating a folder.
type FolderCreateRequest struct {
	Name        string  `json:"name"                  example:"My Folder"`
	Description *string `json:"description,omitempty" example:"Optional description"`
	ParentID    *string `json:"parent_id,omitempty"`
	Ref         string  `query:"ref"                  example:"campaign-123"`
}

// FolderCreateResponse is the response DTO returned after folder creation.
type FolderCreateResponse struct {
	ID          string    `json:"id"          example:"fld_01J3K"`
	Name        string    `json:"name"        example:"My Folder"`
	Description *string   `json:"description,omitempty"`
	ParentID    *string   `json:"parent_id,omitempty"`
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

// FolderTreeRequest demonstrates required:"false", default and description tags.
type FolderTreeRequest struct {
	CompanyID int64  `header:"X-Company-ID" required:"false" default:"0"  description:"Ignore IT! Unimplemented yet! Always uses default value"`
	RootID    *int64 `query:"root"          required:"false"               description:"ID of the root folder. Use 0 for the full company tree"`
	Depth     int    `query:"depth"         required:"false" default:"4"   description:"Maximum depth of the tree. 0 = maximum=32"`
}

// FolderTreeNodeResponse is a single node in the folder tree — self-referential.
type FolderTreeNodeResponse struct {
	ID             int64                     `json:"id"`
	ParentFolderID *int64                    `json:"parent_folder_id"`
	Title          string                    `json:"title"`
	Children       []*FolderTreeNodeResponse `json:"children"`
}

// FolderTreeResponse is the top-level tree response.
type FolderTreeResponse struct {
	Items []*FolderTreeNodeResponse `json:"items"`
}

// CreateUserRequest demonstrates mixed binding sources.
type CreateUserRequest struct {
	Name  string `json:"name"   example:"Alice"`
	Age   int    `json:"age"    example:"30"`
	Ref   string `query:"ref"   required:"false"`
	ID    string `path:"id"`
	Token string `header:"X-Request-Token" required:"false"`
}
