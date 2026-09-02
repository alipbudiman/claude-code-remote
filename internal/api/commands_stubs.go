package api

import "net/http"

// TEMPORARY stubs — replaced by permissions.go / settings.go (Task 7) and
// logs.go (Task 8). They exist so the command dispatcher compiles and tests
// stay green before those tasks land.

func (s *Server) permissionsGet() (map[string]interface{}, error) { return nil, nil }

func (s *Server) permissionsSet(map[string]interface{}) error { return nil }

func (s *Server) persistAppSettings(_ interface{}) {}

func (s *Server) handlePermissions(w http.ResponseWriter, r *http.Request) {
	writeOK(w)
}
