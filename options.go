package websocket

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
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

func WithConnectHeader(header http.Header) Option[Client] {
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

func WithCloseHandler(handler OnCloseHandler) Option[Client] {
	return func(c *Client) {
		c.onClose = handler
	}
}

func WithPingHandler(handler PingHandler) Option[Client] {
	return func(c *Client) {
		c.pingHandler = handler
	}
}

func WithPongHandler(handler PongHandler) Option[Client] {
	return func(c *Client) {
		c.pongHandler = handler
	}
}

func WithCompressionLevel(level int) Option[Client] {
	return func(c *Client) {
		c.compressionLevel = level
	}
}

func WithReadLimit(limit int64) Option[Client] {
	return func(c *Client) {
		c.readLimit = limit
	}
}

func WithHeartbeatInterval(interval time.Duration) Option[Client] {
	return func(c *Client) {
		c.heartbeatInterval = interval
	}
}

func WithHeartbeatHandler(handler HeartbeatHandler) Option[Client] {
	return func(c *Client) {
		c.heartbeat = handler
	}
}

func WithConnected(handler OnConnectedHandler) Option[Client] {
	return func(c *Client) {
		c.onConnected = handler
	}
}

func WithReconnected(handler ReConnectedHandler) Option[Client] {
	return func(c *Client) {
		c.onReconnected = handler
	}
}

func WithBeforeReconnect(handler BeforeReconnectHandler) Option[Client] {
	return func(c *Client) {
		c.beforeReconnect = handler
	}
}

func WithDelayReconnect(delay time.Duration) Option[Client] {
	return func(c *Client) {
		c.delayReconnect = delay
	}
}
