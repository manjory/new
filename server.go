package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/rainbowmga/timetravel/api"
	apiv2 "github.com/rainbowmga/timetravel/api/v2"
	"github.com/rainbowmga/timetravel/service"
)

// logError logs all non-nil errors
func logError(err error) {
	if err != nil {
		log.Printf("error: %v", err)
	}
}

func main() {
	router := mux.NewRouter()

	// Persistent storage. The database file lives next to the binary by
	// default; on first run it will be created automatically.
	recordSvc, err := service.NewSQLiteRecordService("records.db")
	if err != nil {
		log.Fatalf("failed to initialize record store: %v", err)
	}
	defer func() { logError(recordSvc.Close()) }()

	apiHandler := api.NewAPI(recordSvc)

	apiRoute := router.PathPrefix("/api/v1").Subrouter()
	apiRoute.Path("/health").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		logError(err)
	})
	apiHandler.CreateRoutes(apiRoute)

	// v2 endpoints: same backing store, exposes versioning.
	apiV2Handler := apiv2.NewAPI(recordSvc)
	apiV2Route := router.PathPrefix("/api/v2").Subrouter()
	apiV2Handler.CreateRoutes(apiV2Route)

	address := "127.0.0.1:8000"
	srv := &http.Server{
		Handler:      router,
		Addr:         address,
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
	}

	log.Printf("listening on %s", address)
	log.Fatal(srv.ListenAndServe())
}
