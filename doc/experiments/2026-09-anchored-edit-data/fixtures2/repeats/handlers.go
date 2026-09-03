package api

import "net/http"

type Store interface{ Get(string) (string, bool) }

type Server struct{ store Store }

func (s *Server) GetUser(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	v, ok := s.store.Get("user:" + id)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Write([]byte(v))
}

func (s *Server) GetOrder(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	v, ok := s.store.Get("order:" + id)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Write([]byte(v))
}

func (s *Server) GetInvoice(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	v, ok := s.store.Get("invoice:" + id)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Write([]byte(v))
}
