package api

import "net/http"

type ServerDeps struct{}

type Server struct {
	mux *http.ServeMux
}

func NewServer(deps ServerDeps) *Server {
	s := &Server{mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }
