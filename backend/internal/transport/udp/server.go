package udp

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"
)

type PacketHandler interface {
	HandlePacket(ctx context.Context, remoteAddr netip.AddrPort, payload []byte) error
}

type Server struct {
	addr    string
	handler PacketHandler
	logger  *slog.Logger

	mu   sync.Mutex
	conn net.PacketConn
}

func NewServer(addr string, handler PacketHandler, logger *slog.Logger) *Server {
	return &Server{
		addr:    addr,
		handler: handler,
		logger:  logger,
	}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	conn, err := net.ListenPacket("udp", s.addr)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()

	defer func() {
		_ = conn.Close()
	}()

	buffer := make([]byte, 4096)

	for {
		if err := conn.SetReadDeadline(time.Now().Add(1 * time.Second)); err != nil {
			return err
		}

		n, remoteAddr, err := conn.ReadFrom(buffer)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				select {
				case <-ctx.Done():
					return nil
				default:
					continue
				}
			}

			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				select {
				case <-ctx.Done():
					return nil
				default:
					continue
				}
			}

			if errors.Is(err, net.ErrClosed) {
				return nil
			}

			return err
		}

		addrPort, ok := netip.AddrPortFrom(netip.MustParseAddr("0.0.0.0"), 0), false
		if udpAddr, castOK := remoteAddr.(*net.UDPAddr); castOK {
			addrPort, ok = udpAddr.AddrPort(), true
		}
		if !ok {
			s.logger.Warn("received UDP packet from unsupported address type", "remote_addr", remoteAddr.String())
			continue
		}

		payload := make([]byte, n)
		copy(payload, buffer[:n])

		if err := s.handler.HandlePacket(ctx, addrPort, payload); err != nil {
			s.logger.Warn("failed to process UDP packet", "remote_addr", remoteAddr.String(), "error", err)
		}
	}
}

func (s *Server) Shutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn == nil {
		return nil
	}

	err := s.conn.Close()
	s.conn = nil
	return err
}
