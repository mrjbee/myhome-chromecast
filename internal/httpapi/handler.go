package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"myhome-chromecast/internal/castclient"
	"myhome-chromecast/internal/discovery"
)

type Handler struct {
	registry    *discovery.Registry
	connections *castclient.Manager
	logger      *slog.Logger
	mux         *http.ServeMux
}

func NewHandler(registry *discovery.Registry, connections *castclient.Manager, logger *slog.Logger) http.Handler {
	handler := &Handler{
		registry: registry, connections: connections, logger: logger,
		mux: http.NewServeMux(),
	}
	handler.routes()
	return handler
}

func (h *Handler) routes() {
	h.mux.HandleFunc("GET /devices", h.listDevices)
	h.mux.HandleFunc("POST /devices/{id}/connect", h.connect)
	h.mux.HandleFunc("GET /devices/connected", h.connected)
	h.mux.HandleFunc("DELETE /devices/connect", h.disconnect)
	h.mux.HandleFunc("GET /devices/connected/status", h.status)
	h.mux.HandleFunc("GET /devices/connected/volume", h.getVolume)
	h.mux.HandleFunc("PUT /devices/connected/volume", h.setVolume)
	h.mux.HandleFunc("POST /devices/connected/media", h.media)
	h.mux.HandleFunc("POST /devices/connected/youtube", h.youtube)
}

func (h *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	h.mux.ServeHTTP(response, request)
}

func (h *Handler) listDevices(response http.ResponseWriter, _ *http.Request) {
	devices := h.registry.Snapshot()
	views := make([]discovery.DeviceView, len(devices))
	for index, device := range devices {
		views[index] = device.View()
	}
	writeJSON(response, http.StatusOK, views)
}

func (h *Handler) connect(response http.ResponseWriter, request *http.Request) {
	connected, err := h.connections.Connect(request.Context(), request.PathValue("id"))
	if err != nil {
		h.writeOperationError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, connected)
}

func (h *Handler) connected(response http.ResponseWriter, _ *http.Request) {
	connected, err := h.connections.Connected()
	if err != nil {
		h.writeOperationError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, connected)
}

func (h *Handler) disconnect(response http.ResponseWriter, _ *http.Request) {
	if err := h.connections.Disconnect(); err != nil {
		h.writeOperationError(response, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (h *Handler) status(response http.ResponseWriter, request *http.Request) {
	status, err := h.connections.Status(request.Context())
	if err != nil {
		h.writeOperationError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, status)
}

func (h *Handler) getVolume(response http.ResponseWriter, request *http.Request) {
	status, err := h.connections.Status(request.Context())
	if err != nil {
		h.writeOperationError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, status.Status.Volume)
}

func (h *Handler) setVolume(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Level *float32 `json:"level"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	if input.Level == nil {
		writeError(response, http.StatusBadRequest, errors.New("level is required"))
		return
	}
	if *input.Level < 0 || *input.Level > 1 {
		writeError(response, http.StatusBadRequest, errors.New("level must be between 0 and 1"))
		return
	}
	volume, err := h.connections.SetVolume(request.Context(), *input.Level)
	if err != nil {
		if strings.Contains(err.Error(), "between 0 and 1") {
			writeError(response, http.StatusBadRequest, err)
			return
		}
		h.writeOperationError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, volume)
}

func (h *Handler) media(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Command string `json:"command"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	if input.Command != "play" && input.Command != "pause" && input.Command != "stop" {
		writeError(response, http.StatusBadRequest, errors.New("command must be play, pause, or stop"))
		return
	}
	if err := h.connections.MediaCommand(request.Context(), input.Command); err != nil {
		h.writeOperationError(response, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (h *Handler) youtube(response http.ResponseWriter, request *http.Request) {
	var input struct {
		URL string `json:"url"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	videoID, err := h.connections.PlayYouTube(request.Context(), input.URL)
	if err != nil {
		if strings.Contains(err.Error(), "YouTube URL") || strings.Contains(err.Error(), "video ID") {
			writeError(response, http.StatusBadRequest, err)
			return
		}
		h.writeOperationError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"videoId": videoID})
}

func (h *Handler) writeOperationError(response http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	if errors.Is(err, castclient.ErrNotConnected) {
		status = http.StatusConflict
	} else if errors.Is(err, castclient.ErrConnectedDeviceNotPresent) {
		status = http.StatusNotFound
	} else if strings.Contains(err.Error(), "not found") {
		status = http.StatusNotFound
	}
	h.logger.Warn("request failed", "status", status, "error", err)
	writeError(response, status, err)
}

func decodeJSON(request *http.Request, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON object")
	}
	return nil
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, err error) {
	writeJSON(response, status, map[string]string{"error": err.Error()})
}
