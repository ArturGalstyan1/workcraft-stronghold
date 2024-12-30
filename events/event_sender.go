package events

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
)

type EventSender struct {
	connections map[string]http.ResponseWriter
	controllers map[string]http.ResponseController
	mu          sync.RWMutex
}

func NewEventSender() *EventSender {
	return &EventSender{
		connections: make(map[string]http.ResponseWriter),
		controllers: make(map[string]http.ResponseController),
	}
}

func (s *EventSender) Register(ID string, w http.ResponseWriter, rc http.ResponseController) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connections[ID] = w
	s.controllers[ID] = rc
}

func (s *EventSender) Unregister(ID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.connections, ID)
	delete(s.controllers, ID)
}

func (s *EventSender) SendEvent(ID string, msg string) error {
	s.mu.RLock()
	w, exists := s.connections[ID]
	rc, _ := s.controllers[ID]
	s.mu.RUnlock()

	if !exists {
		return fmt.Errorf("no connection found for ID: %s", ID)
	}

	_, err := fmt.Fprintf(w, "data: %s\n\n", msg)
	if err != nil {
		return err
	}
	return rc.Flush()
}

func (s *EventSender) BroadcastToChieftains(msg string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for id := range s.connections {
		if strings.HasPrefix(id, "chieftain-") {
			w := s.connections[id]
			rc := s.controllers[id]
			_, err := fmt.Fprintf(w, "data: %s\n\n", msg)
			if err != nil {
				slog.Error("Failed to write to writer", "err", err)
				continue
			}
			rc.Flush()
		}
	}
}
