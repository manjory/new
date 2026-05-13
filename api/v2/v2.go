// Package v2 implements the /api/v2 record endpoints.
//
// V2 layers history on top of the v1 storage model. Reads return the
// richer VersionedRecord shape (id + version + created_at + data) and
// writes return the freshly-created version's metadata. Backward
// compatibility for v1 is preserved by keeping that package untouched.
package v2

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/rainbowmga/timetravel/entity"
	"github.com/rainbowmga/timetravel/service"
)

// API holds the dependencies for the v2 handlers.
type API struct {
	records service.VersionedRecordService
}

// NewAPI constructs a v2 API. The service must support versioned reads;
// the SQLite-backed implementation does.
func NewAPI(records service.VersionedRecordService) *API {
	return &API{records: records}
}

// CreateRoutes registers all v2 routes onto the given subrouter.
func (a *API) CreateRoutes(routes *mux.Router) {
	routes.Path("/records/{id}").HandlerFunc(a.GetLatest).Methods("GET")
	routes.Path("/records/{id}").HandlerFunc(a.Post).Methods("POST")
	routes.Path("/records/{id}/versions").HandlerFunc(a.ListVersions).Methods("GET")
	routes.Path("/records/{id}/versions/{version}").HandlerFunc(a.GetVersion).Methods("GET")
}

// --- handlers ---------------------------------------------------------------

// GET /api/v2/records/{id}
// Returns the latest version of the record in the v2 envelope.
func (a *API) GetLatest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, ok := parseID(w, r)
	if !ok {
		return
	}

	record, err := a.records.GetLatestVersion(ctx, id)
	if errors.Is(err, service.ErrRecordDoesNotExist) {
		writeError(w, fmt.Sprintf("record of id %d does not exist", id), http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("v2 GetLatest error: %v", err)
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, record, http.StatusOK)
}

// GET /api/v2/records/{id}/versions/{version}
// Returns one specific historical version of the record.
func (a *API) GetVersion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, ok := parseID(w, r)
	if !ok {
		return
	}

	versionStr := mux.Vars(r)["version"]
	versionNum, err := strconv.Atoi(versionStr)
	if err != nil || versionNum <= 0 {
		writeError(w, "invalid version; must be a positive integer", http.StatusBadRequest)
		return
	}

	record, err := a.records.GetRecordVersion(ctx, id, versionNum)
	switch {
	case errors.Is(err, service.ErrRecordDoesNotExist):
		writeError(w, fmt.Sprintf("record of id %d does not exist", id), http.StatusNotFound)
		return
	case errors.Is(err, service.ErrVersionDoesNotExist):
		writeError(w, fmt.Sprintf("record of id %d has no version %d", id, versionNum), http.StatusNotFound)
		return
	case err != nil:
		log.Printf("v2 GetVersion error: %v", err)
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, record, http.StatusOK)
}

// GET /api/v2/records/{id}/versions
// Returns the list of versions (version number + timestamp) for a record.
func (a *API) ListVersions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, ok := parseID(w, r)
	if !ok {
		return
	}

	versions, err := a.records.ListVersions(ctx, id)
	if errors.Is(err, service.ErrRecordDoesNotExist) {
		writeError(w, fmt.Sprintf("record of id %d does not exist", id), http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("v2 ListVersions error: %v", err)
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{
		"id":       id,
		"versions": versions,
	}, http.StatusOK)
}

// POST /api/v2/records/{id}
// Creates or updates a record and returns the new version metadata.
//
// Same semantics as v1's POST -- null values delete keys -- but the
// response is the richer VersionedRecord shape including version and
// created_at.
func (a *API) Post(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, ok := parseID(w, r)
	if !ok {
		return
	}

	var body map[string]*string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid input; could not parse json", http.StatusBadRequest)
		return
	}

	// Try update first; fall back to create if the record doesn't exist.
	_, err := a.records.UpdateRecord(ctx, id, body)
	if errors.Is(err, service.ErrRecordDoesNotExist) {
		// Create. Drop null-valued keys -- those are "delete" instructions
		// and have no meaning on first creation.
		initial := map[string]string{}
		for k, v := range body {
			if v != nil {
				initial[k] = *v
			}
		}
		if err := a.records.CreateRecord(ctx, entity.Record{ID: id, Data: initial}); err != nil {
			log.Printf("v2 Post create error: %v", err)
			writeError(w, "internal error", http.StatusInternalServerError)
			return
		}
	} else if err != nil {
		log.Printf("v2 Post update error: %v", err)
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Either way, the latest version is what the caller wants to see back.
	latest, err := a.records.GetLatestVersion(ctx, id)
	if err != nil {
		log.Printf("v2 Post post-read error: %v", err)
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, latest, http.StatusOK)
}

// --- shared helpers (private to package v2) --------------------------------

func parseID(w http.ResponseWriter, r *http.Request) (int, bool) {
	idStr := mux.Vars(r)["id"]
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		writeError(w, "invalid id; id must be a positive number", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, data any, statusCode int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("v2 writeJSON error: %v", err)
	}
}

func writeError(w http.ResponseWriter, message string, statusCode int) {
	writeJSON(w, map[string]string{"error": message}, statusCode)
}
