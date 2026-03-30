package handler

import (
	"fmt"
	"net/http"
)

func NewFolderController() *FolderController {
	return &FolderController{}
}

type FolderController struct{}

func (c *FolderController) Create(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "Create folder")
}

func (c *FolderController) Get(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "Get folder")
}

func (c *FolderController) List(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "List folders")
}
