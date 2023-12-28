package websocket

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type clientOpts struct {
	logger           *slog.Logger
	dialer           *websocket.Dialer
	proxyURL         string
	header           http.Header
	compression      bool
	compressionLevel int
	autoReconnect    bool
	readLimit        int64

	readTimeout       time.Duration
	writeTimeout      time.Duration
	connectTimeout    time.Duration
	heartbeatInterval time.Duration

	onConnected   OnConnectedHandler
	onReconnected ReConnectedHandler
	decompress    DecompressHandler
	onClose       OnCloseHandler
	pingHandler   PingHandler
	pongHandler   PongHandler
	errHandler    ErrorHandler
	heartbeat     HeartbeatHandler
}

func newOpts() *clientOpts {
	opts := new(clientOpts)
	opts.logger = slog.Default()
	opts.readTimeout = time.Second * 3
	opts.writeTimeout = time.Second * 3
	opts.connectTimeout = time.Second * 30
	opts.heartbeatInterval = time.Minute
	opts.onConnected = func(*Client) {}
	opts.onReconnected = func(*Client) {}
	return opts
}

type Message struct {
	Type FrameType
	Msg  []byte
}

type Client struct {
	*clientOpts
	ctx      context.Context
	wsURL    string
	conn     *websocket.Conn
	stopC    chan struct{}
	writeC   chan Message
	receiver Receiver
	sdOnce   sync.Once
}

func NewClient(ctx context.Context, wsURL string, receiver Receiver, opts ...Option[Client]) (*Client, error) {
	if receiver == nil {
		return nil, errors.New("can't using nil receiver")
	}

	client := new(Client)
	client.ctx = ctx
	client.clientOpts = newOpts()
	client.receiver = receiver
	client.wsURL = wsURL
	client.errHandler = client.defaultErrorHandler
	applyOpts[Client](client, opts)
	client.stopC = make(chan struct{})
	client.writeC = make(chan Message, 10)
	if err := client.connect(); err != nil {
		return nil, err
	}
	client.logger.Info("Websocket connected", slog.String("URL", wsURL))
	return client, nil
}

func (c *Client) Shutdown() (err error) {
	c.sdOnce.Do(func() {
		close(c.stopC)
		if c.conn != nil {
			err = c.conn.Close()
		}
		c.logger.Info("Websocket stopped.", slog.String("URL", c.wsURL))
	})
	return
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

	if err = c.conn.SetCompressionLevel(c.compressionLevel); err != nil {
		return err
	}

	go c.readLoop()
	go c.writeLoop()
	c.onConnected(c)
	return
}

func (c *Client) reconnect() {
	if err := c.connect(); err != nil {
		return
	}

	c.onReconnected(c)
}

func (c *Client) getWriteDeadline() time.Time {
	return time.Now().Add(c.writeTimeout)
}

func (c *Client) SendMessage(message []byte) {
	c.writeC <- Message{Type: FrameTypeText, Msg: message}
}

func (c *Client) SendBinary(message []byte) {
	c.writeC <- Message{Type: FrameTypeBinary, Msg: message}
}

func (c *Client) SendJson(message any) error {
	bs, err := json.Marshal(message)
	if err != nil {
		return err
	}
	c.writeC <- Message{Type: FrameTypeText, Msg: bs}
	return nil
}

func (c *Client) SendPing(message []byte) {
	c.writeC <- Message{Type: FrameTypePing, Msg: message}
}

func (c *Client) SendPong(message []byte) {
	c.writeC <- Message{Type: FrameTypePong, Msg: message}
}

func (c *Client) SendClose(message []byte) {
	c.writeC <- Message{Type: FrameTypeClose, Msg: message}
}

func (c *Client) writeLoop() {
	ticker := time.NewTicker(c.heartbeatInterval)

	for {
		select {
		case <-c.stopC:
			_ = c.Shutdown()
			return
		case <-c.ctx.Done():
			_ = c.Shutdown()
			return
		case <-ticker.C:
			if c.heartbeat != nil {
				c.heartbeat(c)
			}
		case msg := <-c.writeC:
			var err error
			switch msg.Type {
			case FrameTypePing, FrameTypePong, FrameTypeClose:
				err = c.conn.WriteControl(int(msg.Type), msg.Msg, c.getWriteDeadline())
			case FrameTypeText, FrameTypeBinary:
				_ = c.conn.SetWriteDeadline(c.getWriteDeadline())
				err = c.conn.WriteMessage(int(msg.Type), msg.Msg)
			}

			if err != nil {
				c.errHandler(msg.Type, errors.Wrap(err, "failed to send message"))
			}
		}
	}
}

func (c *Client) readLoop() {
	c.conn.SetReadLimit(c.readLimit)
	c.conn.SetCloseHandler(c.onClose)
	c.conn.SetPingHandler(c.pingHandler)
	c.conn.SetPongHandler(c.pongHandler)

	readFrame := func() bool {
		defer func() {
			if err := recover(); err != nil {
				c.logger.Error("failed to process",
					slog.Any("err", err))
			}
		}()

		select {
		case <-c.stopC:
			_ = c.Shutdown()
			return true
		case <-c.ctx.Done():
			_ = c.Shutdown()
			return true
		default:
			_ = c.conn.SetReadDeadline(time.Now().Add(c.readTimeout))
			ft, reader, err := c.conn.NextReader()
			if err != nil {
				if c.autoReconnect {
					c.reconnect()
				} else {
					_ = c.Shutdown()
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
