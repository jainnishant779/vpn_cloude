package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type responseEnvelope struct {
	Success bool   `json:"success"`
	Data    any    `json:"data"`
	Error   string `json:"error"`
}

func writeSuccess(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, responseEnvelope{
		Success: true,
		Data:    data,
		Error:   "",
	})
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, responseEnvelope{
		Success: false,
		Data:    nil,
		Error:   message,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload responseEnvelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func decodeJSONBody(r *http.Request, dest any) error {
	if r.Body == nil {
		return fmt.Errorf("request body is required")
	}
	defer r.Body.Close()

	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return fmt.Errorf("decode json body: %w", err)
	}

	if decoder.More() {
		return fmt.Errorf("decode json body: multiple JSON objects are not allowed")
	}

	return nil
}
