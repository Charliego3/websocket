package websocket

import (
	"context"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"
)

var (
	mutex sync.Mutex
	conns = make(map[string]*Client)
)

type clientOpts struct {
	logger        *slog.Logger
	dialer        *websocket.Dialer
	proxyURL      string
	header        http.Header
	compression   bool
	autoReconnect bool

	readTimeout    time.Duration
	writeTimeout   time.Duration
	connectTimeout time.Duration

	onConnected   OnConnectedHandler
	onReconnected ReConnectedHandler
	decompress    DecompressHandler
	errHandler    ErrorHandler
}

func newOpts() *clientOpts {
	opts := new(clientOpts)
	opts.logger = slog.Default()
	opts.readTimeout = time.Second * 30
	opts.writeTimeout = time.Second * 3
	opts.connectTimeout = time.Second * 30
	opts.onConnected = func(*Client) {}
	opts.onReconnected = func(*Client) {}
	return opts
}

type Client struct {
	*clientOpts
	ctx      context.Context
	wsURL    string
	conn     *websocket.Conn
	status   Status
	stopC    chan struct{}
	receiver Receiver
}

func NewClient(ctx context.Context, wsURL string, receiver Receiver, opts ...Option[Client]) (*Client, error) {
	if receiver == nil {
		return nil, errors.New("can't using nil receiver")
	}

	if c, ok := conns[wsURL]; ok {
		return c, nil
	}

	mutex.Lock()
	defer mutex.Unlock()

	if c, ok := conns[wsURL]; ok {
		return c, nil
	}

	client := new(Client)
	client.ctx = ctx
	client.clientOpts = newOpts()
	client.receiver = receiver
	client.wsURL = wsURL
	client.errHandler = client.defaultErrorHandler
	applyOpts[Client](client, opts)
	client.status = StatusConnecting
	client.stopC = make(chan struct{})
	if err := client.connect(); err != nil {
		return nil, err
	}
	client.status = StatusConnected
	client.onConnected(client)
	conns[wsURL] = client
	client.logger.Info("Websocket connected", slog.String("URL", wsURL))
	return client, nil
}

func (c *Client) connect() (err error) {
	dialer, err := c.getDialer()
	if err != nil {
		return err
	}

	c.conn, _, err = dialer.Dial(c.wsURL, c.header)
	if err != nil {
		return
	}

	go c.readLoop()
	return
}

func (c *Client) reconnect() {
	c.status = StatusReConnecting
	if err := c.connect(); err != nil {
		return
	}

	c.status = StatusConnected
	c.onReconnected(c)
}

func (c *Client) setWriteDeadLine() {
	_ = c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
}

func (c *Client) SendMessage(message []byte) error {
	c.setWriteDeadLine()
	return c.conn.WriteMessage(websocket.TextMessage, message)
}

func (c *Client) SendJson(message any) error {
	c.setWriteDeadLine()
	return c.conn.WriteJSON(message)
}

func (c *Client) SendPing(message []byte) error {
	c.setWriteDeadLine()
	return c.conn.WriteMessage(websocket.PingMessage, message)
}

func (c *Client) SendPong(message []byte) error {
	c.setWriteDeadLine()
	return c.conn.WriteMessage(websocket.PongMessage, message)
}

func (c *Client) SendClose(message []byte) error {
	c.setWriteDeadLine()
	return c.conn.WriteMessage(websocket.CloseMessage, message)
}

func (c *Client) readLoop() {
	//c.conn.SetPingHandler(nil)
	//c.conn.SetPongHandler(func(appData string) error {
	//	return nil
	//})

	readFrame := func() bool {
		defer func() {
			if err := recover(); err != nil {
				c.logger.Error("failed to process received message",
					slog.Any("err", err))
			}
		}()

		select {
		case <-c.stopC:
			c.logger.Info("websocket accept is stopped")
			return true
		case <-c.ctx.Done():
			c.logger.Info("received stop command")
			return true
		default:
			_ = c.conn.SetReadDeadline(time.Now().Add(c.readTimeout))
			ft, reader, err := c.conn.NextReader()
			if err != nil {
				if c.autoReconnect {
					c.reconnect()
				} else {
					_ = c.conn.Close()
				}
				return true
			}

			frameType := FrameType(ft)
			if frameType != FrameTypeText && frameType != FrameTypeBinary {
				return false
			}

			if frameType == FrameTypeBinary && c.decompress != nil {
				if reader, err = c.decompress(reader); err != nil {
					c.errHandler(frameType, errors.Wrap(err, "failed to decompress frame"))
					return false
				}
			}

			frame, per := c.receiver.Unmarshal(frameType, reader)
			if per != nil {
				c.errHandler(frameType, errors.Wrap(per, "failed to parse frame"))
				return false
			}
			c.receiver.OnMessage(frame)
			return false
		}
	}

	for {
		if readFrame() {
			return
		}
	}
}

func (c *Client) defaultErrorHandler(frameType FrameType, err error) {
	c.logger.Error("websocket got an error",
		slog.String("URL", c.wsURL),
		slog.String("frameType", frameType.String()),
		slog.Any("err", err),
	)
}

func (o *clientOpts) getDialer() (*websocket.Dialer, error) {
	if o.dialer == nil {
		o.dialer = &websocket.Dialer{
			Proxy:             http.ProxyFromEnvironment,
			HandshakeTimeout:  o.connectTimeout,
			EnableCompression: o.compression,
		}

		if o.proxyURL != "" {
			proxy, err := url.Parse(o.proxyURL)
			if err != nil {
				return nil, err
			}
			o.dialer.Proxy = http.ProxyURL(proxy)
		}
	}

	return o.dialer, nil
}
