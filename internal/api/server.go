package api

import (
	"encoding/json"
	"net/http"
	"time"

	"phoenix-v3/internal/feed"
	"phoenix-v3/internal/strategy"
)

type Server struct {
	queue   *strategy.IntentQueue
	lastCEX *feed.Ticker
	status  *SystemStatus
}

type SystemStatus struct {
	Healthy     bool      `json:"healthy"`
	LastUpdate  time.Time `json:"last_update"`
	EngineState string    `json:"engine_state"`
}

func NewServer(q *strategy.IntentQueue) *Server {
	return &Server{
		queue: q,
		status: &SystemStatus{
			Healthy:     true,
			LastUpdate:  time.Now(),
			EngineState: "Running",
		},
	}
}

// UpdateCEXPrice updates the internal state for serving
func (s *Server) UpdateCEXPrice(t feed.Ticker) {
	s.lastCEX = &t
	s.status.LastUpdate = time.Now()
}

func (s *Server) Start(port string) {
	// CORS middleware
	cors := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			next(w, r)
		}
	}

	http.HandleFunc("/api/status", cors(s.handleStatus))
	http.HandleFunc("/api/intents", cors(s.handleIntents))

	go http.ListenAndServe(":"+port, nil)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	price := 0.0
	if s.lastCEX != nil {
		price = s.lastCEX.Price
	}

	resp := map[string]interface{}{
		"system": s.status,
		"market": map[string]interface{}{
			"price":  price,
			"symbol": "ETH/USDT",
		},
	}
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleIntents(w http.ResponseWriter, r *http.Request) {
	// Copy queue logic (this is a bit hacky, normally we'd list them)
	// For now, return a placeholder count
	count := s.queue.Len()
	json.NewEncoder(w).Encode(map[string]int{
		"pending_count": count,
	})
}
