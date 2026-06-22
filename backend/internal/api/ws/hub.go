package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
)

type Hub struct {
	logger     *slog.Logger
	register   chan *Client
	unregister chan *Client
	broadcast  chan []byte
	clients    map[*Client]struct{}
	closeOnce  sync.Once
	closed     chan struct{}
}

func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		logger:     logger,
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan []byte, 128),
		clients:    make(map[*Client]struct{}),
		closed:     make(chan struct{}),
	}
}

func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = struct{}{}
			h.logger.Info("websocket client connected", "clients", len(h.clients))
		case client := <-h.unregister:
			if _, exists := h.clients[client]; exists {
				delete(h.clients, client)
				close(client.send)
				h.logger.Info("websocket client disconnected", "clients", len(h.clients))
			}
		case payload := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- payload:
				default:
					delete(h.clients, client)
					close(client.send)
				}
			}
		case <-h.closed:
			for client := range h.clients {
				close(client.send)
				delete(h.clients, client)
			}
			return
		case <-ctx.Done():
			h.Close()
		}
	}
}

func (h *Hub) BroadcastJSON(v any) {
	payload, err := json.Marshal(v)
	if err != nil {
		h.logger.Error("failed to marshal websocket payload", "error", err)
		return
	}

	select {
	case h.broadcast <- payload:
	case <-h.closed:
	}
}

func (h *Hub) Close() {
	h.closeOnce.Do(func() {
		close(h.closed)
	})
}
