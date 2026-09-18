package httpapi

import (
	"net/http"
)

// Подключения к моделям управляются так же, как подключения к БД: список,
// удаление, проверка. До этого файла существовал ровно один маршрут — upsert, —
// поэтому исправить опечатку в адресе или убрать лишний ключ было нечем, а
// выбор нужного подключения оставался угадыванием по пресету.
func (s *Server) registerConnectionRoutes() {
	s.mux.HandleFunc("GET /api/connections", s.listConnections)
	s.mux.HandleFunc("DELETE /api/connections/{id}", s.deleteConnection)
	s.mux.HandleFunc("POST /api/connections/{id}/probe", s.probeConnection)
	s.mux.HandleFunc("POST /api/connections/{id}/default", s.defaultConnection)
}

func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ListConnections()
	s.result(w, value, err)
}

func (s *Server) deleteConnection(w http.ResponseWriter, r *http.Request) {
	err := s.app.DeleteConnection(r.PathValue("id"))
	s.result(w, map[string]any{"ok": true}, err)
}

// Ключ приходит транзитом из SecretStorage расширения: в SQLite и журнал
// попадает только каталог моделей и статус.
func (s *Server) probeConnection(w http.ResponseWriter, r *http.Request) {
	var input struct {
		APIKey string `json:"apiKey"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ProbeConnection(r.PathValue("id"), input.APIKey)
	s.result(w, value, err)
}

func (s *Server) defaultConnection(w http.ResponseWriter, r *http.Request) {
	err := s.app.SetDefaultConnection(r.PathValue("id"))
	s.result(w, map[string]any{"ok": true}, err)
}
