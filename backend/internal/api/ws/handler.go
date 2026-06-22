package ws

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

type Handler struct {
	hub      *Hub
	upgrader websocket.Upgrader
}

func NewHandler(hub *Hub, readBufferSize, writeBufferSize int) *Handler {
	if readBufferSize <= 0 {
		readBufferSize = 1024
	}
	if writeBufferSize <= 0 {
		writeBufferSize = 1024
	}

	return &Handler{
		hub: hub,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  readBufferSize,
			WriteBufferSize: writeBufferSize,
			CheckOrigin: func(_ *http.Request) bool {
				return true
			},
		},
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &Client{
		hub:  h.hub,
		conn: conn,
		send: make(chan []byte, 64),
	}

	h.hub.register <- client

	go client.writeLoop()
	client.readLoop()
}

func (c *Client) readLoop() {
	defer func() {
		c.hub.unregister <- c
		_ = c.conn.Close()
	}()

	for {
		if _, _, err := c.conn.NextReader(); err != nil {
			return
		}
	}
}

func (c *Client) writeLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
