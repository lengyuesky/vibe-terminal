package terminal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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

type OutputWriter interface {
	WriteChunk(sessionID string, startSeq int64, endSeq int64, data []byte) (path string, bytesWritten int64, err error)
}

type OutputStore interface {
	OutputWriter
	ReadChunk(storagePath string) ([]byte, error)
}

type FileOutputWriter struct {
	Root string
}

func (w FileOutputWriter) WriteChunk(sessionID string, startSeq int64, endSeq int64, data []byte) (string, int64, error) {
	relativeDir := filepath.Join("sessions", safeName(sessionID))
	dir := filepath.Join(w.Root, relativeDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, err
	}
	filename := fmt.Sprintf("%012d-%012d.log", startSeq, endSeq)
	relativePath := filepath.Join(relativeDir, filename)
	fullPath := filepath.Join(w.Root, relativePath)
	if err := os.WriteFile(fullPath, data, 0o600); err != nil {
		return "", 0, err
	}
	return filepath.ToSlash(relativePath), int64(len(data)), nil
}

func (w FileOutputWriter) ReadChunk(storagePath string) ([]byte, error) {
	cleanPath := filepath.Clean(storagePath)
	if filepath.IsAbs(cleanPath) || cleanPath == "." || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) || cleanPath == ".." {
		return nil, fmt.Errorf("invalid storage path")
	}
	return os.ReadFile(filepath.Join(w.Root, cleanPath))
}

func safeName(value string) string {
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, "\\", "_")
	value = strings.ReplaceAll(value, "..", "_")
	return value
}
