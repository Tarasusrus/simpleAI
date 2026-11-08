package web

import (
	"context"
	"net/http"

	"simpleAI/internal/plan"
)

// WindowEditor — правка окна на экране (ADR-020). Узкий интерфейс потребителя: веб не знает, как окно хранится.
type WindowEditor interface {
	ResizeWindow(ctx context.Context, days int) (plan.WindowFrame, error)
	SetWindowEnd(ctx context.Context, end plan.Date) (plan.WindowFrame, error)
	ResetWindow(ctx context.Context) (plan.WindowFrame, error)
}

// WithWindowEditor подключает правку окна. Без неё маршруты отвечают 404.
func (s *Server) WithWindowEditor(e WindowEditor) *Server {
	s.windowEditor = e
	return s
}

type resizeBody struct {
	// Days — на сколько дней продлить (>0) или сократить (<0) окно.
	Days int `json:"days"`
}

type windowEndBody struct {
	End string `json:"end"`
}

func (s *Server) registerWindowRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/plan/window/resize", s.auth(http.HandlerFunc(s.handleWindowResize)))
	mux.Handle("PUT /api/plan/window/end", s.auth(http.HandlerFunc(s.handleWindowEnd)))
	mux.Handle("DELETE /api/plan/window/length", s.auth(http.HandlerFunc(s.handleWindowReset)))
}

func (s *Server) windowEditorOn(w http.ResponseWriter) bool {
	if s.windowEditor == nil {
		writeError(w, http.StatusNotFound, "Правка окна не включена.")
		return false
	}
	return true
}

// windowEdited отвечает после правки свежим окном, как и остальные команды экрана.
func (s *Server) windowEdited(w http.ResponseWriter, r *http.Request, command string, err error) {
	if err != nil {
		s.writeCommandError(w, err)
		return
	}
	s.logger.Info("plan web command", "command", command, "added_by", "web")
	s.respondView(w, r, http.StatusOK)
}

// handleWindowResize: POST /api/plan/window/resize {"days": ±N}
func (s *Server) handleWindowResize(w http.ResponseWriter, r *http.Request) {
	if !s.windowEditorOn(w) {
		return
	}
	var b resizeBody
	if !decode(w, r, &b) {
		return
	}
	_, err := s.windowEditor.ResizeWindow(r.Context(), b.Days)
	s.windowEdited(w, r, "window_resize", err)
}

// handleWindowEnd: PUT /api/plan/window/end {"end": "YYYY-MM-DD"}
func (s *Server) handleWindowEnd(w http.ResponseWriter, r *http.Request) {
	if !s.windowEditorOn(w) {
		return
	}
	var b windowEndBody
	if !decode(w, r, &b) {
		return
	}
	end, err := plan.ParseDate(b.End)
	if err == nil {
		_, err = s.windowEditor.SetWindowEnd(r.Context(), end)
	}
	s.windowEdited(w, r, "window_end", err)
}

// handleWindowReset: DELETE /api/plan/window/length — окну снова 14 дней.
func (s *Server) handleWindowReset(w http.ResponseWriter, r *http.Request) {
	if !s.windowEditorOn(w) {
		return
	}
	_, err := s.windowEditor.ResetWindow(r.Context())
	s.windowEdited(w, r, "window_reset", err)
}
