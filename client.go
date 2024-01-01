package websocket

import (
	"context"
	"encoding/json"
	stderrs "errors"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

var (
	mutex sync.Mutex
	conns = make(map[string]*Client)
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
	status   Status
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
	client.stopC = make(chan struct{})
	client.writeC = make(chan Message, 10)
	client.status = StatusConnecting
	if err := client.connect(); err != nil {
		return nil, err
	}
	client.status = StatusConnected
	go client.readLoop()
	go client.writeLoop()
	client.onConnected(client)
	conns[wsURL] = client
	client.logger.Info("Websocket connected", slog.String("URL", wsURL))
	return client, nil
}

func (c *Client) setStatus(status Status) {
	atomic.StoreUint32((*uint32)(&c.status), uint32(status))
}

func (c *Client) Shutdown() (err error) {
	c.sdOnce.Do(func() {
		mutex.Lock()
		defer mutex.Unlock()
		c.setStatus(StatusDisconnecting)
		close(c.stopC)
		delete(conns, c.wsURL)
		if c.conn != nil {
			err = c.conn.Close()
		}
		c.setStatus(StatusDisconnected)
		c.logger.Info("Websocket stopped.", slog.String("URL", c.wsURL))
	})
	return
}

func (c *Client) connect() (err error) {
	if c.dialer == nil {
		c.dialer = &websocket.Dialer{
			Proxy:             http.ProxyFromEnvironment,
			HandshakeTimeout:  c.connectTimeout,
			EnableCompression: c.compression,
		}

		if c.proxyURL != "" {
			proxy, err := url.Parse(c.proxyURL)
			if err != nil {
				return err
			}
			c.dialer.Proxy = http.ProxyURL(proxy)
		}
	}

	c.conn, _, err = c.dialer.Dial(c.wsURL, c.header)
	if err != nil {
		return
	}

	if err = c.conn.SetCompressionLevel(c.compressionLevel); err != nil {
		return err
	}
	return
}

func (c *Client) reconnect() {
	c.logger.Info("Websocket reconnecting", slog.String("wsURL", c.wsURL))
	c.setStatus(StatusReConnecting)
	if err := c.connect(); err != nil {
		return
	}
	c.setStatus(StatusConnected)
	c.onReconnected(c)
	c.logger.Info("Websocket reconnected", slog.String("wsURL", c.wsURL))
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
		status := Status(atomic.LoadUint32((*uint32)(&c.status)))
		if status == StatusConnecting || status == StatusReConnecting {
			c.logger.Debug("Websocket reconnecting stop write", slog.String("wsURL", c.wsURL))
			time.Sleep(time.Millisecond * 500)
			continue
		}

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
				c.logger.Debug("Websocket send", slog.String("message", string(msg.Msg)))
				err = c.conn.WriteMessage(int(msg.Type), msg.Msg)
			}

			if err != nil {
				var ope *net.OpError
				c.logger.Error("Websocket failed send message", slog.String("wsURL", c.wsURL), slog.Any("err", err))
				if stderrs.As(err, &ope) {
					c.reconnect()
					continue
				}
				c.errHandler(msg.Type, errors.Wrap(err, "Websocket failed send message"))
			}
		}
	}
}

func (c *Client) readLoop() {
	c.conn.SetReadLimit(c.readLimit)
	if c.onClose != nil {
		c.conn.SetCloseHandler(c.onClose)
	}
	if c.pingHandler != nil {
		c.conn.SetPingHandler(c.pingHandler)
	}
	if c.pongHandler != nil {
		c.conn.SetPongHandler(c.pongHandler)
	}

	readFrame := func() bool {
		defer func() {
			if err := recover(); err != nil {
				c.logger.Error("Websocket failed read message",
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
				c.logger.Error("Websocket read message error", slog.String("wsURL", c.wsURL), slog.Any("err", err))
				status := Status(atomic.LoadUint32((*uint32)(&c.status)))
				if status == StatusDisconnecting || status == StatusDisconnected {
					return true
				}
				if c.autoReconnect {
					c.reconnect()
					return false
				} else {
					_ = c.Shutdown()
					return true
				}
			}

			frameType := FrameType(ft)
			if frameType != FrameTypeText && frameType != FrameTypeBinary {
				return false
			}

			if frameType == FrameTypeBinary && c.decompress != nil {
				if reader, err = c.decompress(reader); err != nil {
					c.errHandler(frameType, errors.Wrap(err, "Websocket failed decompress frame"))
					return false
				}
			}

			frame, per := c.receiver.Unmarshal(frameType, reader)
			if per != nil {
				c.errHandler(frameType, errors.Wrap(per, "Websocket failed parse frame"))
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
	c.logger.Error("Websocket got an error",
		slog.String("URL", c.wsURL),
		slog.String("frameType", frameType.String()),
		slog.Any("err", err),
	)
}
