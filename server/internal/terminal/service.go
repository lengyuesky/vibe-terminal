package terminal

import "errors"

var ErrDeviceOffline = errors.New("device offline")

type CreateRequest struct {
	ShellPath        string `json:"shell_path"`
	WorkingDirectory string `json:"working_directory"`
	Cols             int    `json:"cols"`
	Rows             int    `json:"rows"`
}

func NormalizeCreateRequest(req CreateRequest) CreateRequest {
	if req.ShellPath == "" {
		req.ShellPath = "/bin/bash"
	}
	if req.WorkingDirectory == "" {
		req.WorkingDirectory = "$HOME"
	}
	if req.Cols <= 0 {
		req.Cols = 80
	}
	if req.Rows <= 0 {
		req.Rows = 24
	}
	return req
}
