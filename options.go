package websocket

import (
	"github.com/gorilla/websocket"
	"log/slog"
	"net/http"
	"time"
)

type Option[T any] func(*T)

func (o Option[T]) apply(t *T) {
	o(t)
}

func applyOpts[T any](t *T, opts []Option[T]) {
	for _, opt := range opts {
		opt.apply(t)
	}
}

// Client options

// WithLogger custom specify logger instance
func WithLogger(logger *slog.Logger) Option[Client] {
	return func(c *Client) {
		c.logger = logger
	}
}

func WithDialer(dialer websocket.Dialer) Option[Client] {
	return func(c *Client) {
		c.dialer = &dialer
	}
}

func WithRequestHeader(header http.Header) Option[Client] {
	return func(c *Client) {
		c.header = header
	}
}

func WithProxyURL(proxyURL string) Option[Client] {
	return func(c *Client) {
		c.proxyURL = proxyURL
	}
}

func WithCompression(compression bool) Option[Client] {
	return func(c *Client) {
		c.compression = compression
	}
}

func WithAutoReConnect() Option[Client] {
	return func(c *Client) {
		c.autoReconnect = true
	}
}

func WithReadTimeout(timeout time.Duration) Option[Client] {
	return func(c *Client) {
		c.readTimeout = timeout
	}
}

func WithConnectTimeout(timeout time.Duration) Option[Client] {
	return func(c *Client) {
		c.connectTimeout = timeout
	}
}

func WithDecompressHandler(handler DecompressHandler) Option[Client] {
	return func(c *Client) {
		c.decompress = handler
	}
}

func WithErrorHandler(handler ErrorHandler) Option[Client] {
	return func(c *Client) {
		c.errHandler = handler
	}
}

func WithMessageHandler(handler OnMessageHandler) Option[Client] {
	return func(c *Client) {
		c.onMessage = handler
	}
}

func WithMessageParser(parser Parser) Option[Client] {
	return func(c *Client) {
		c.parser = parser
	}
}
