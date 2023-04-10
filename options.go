package websocket

import (
	"github.com/charmbracelet/log"
	"time"
)

type Options struct {
	timeout          time.Duration
	period           time.Duration
	ping             IMessage
	logger           *log.Logger
	prefix           string
	onConnected      func(client *Client)
	hideURL          bool
	disableReconnect bool
}

type Option func(opts *Options)

func WithDialTimeout(timeout time.Duration) Option {
	return func(opts *Options) {
		opts.timeout = timeout
	}
}

func WithPingPeriod(period time.Duration) Option {
	return func(opts *Options) {
		opts.period = period
	}
}

func WithPing(msg IMessage) Option {
	return func(opts *Options) {
		opts.ping = msg
	}
}

func WithLogger(logger *log.Logger) Option {
	return func(opts *Options) {
		opts.logger = logger
	}
}

func WithOnConnected(f func(*Client)) Option {
	return func(opts *Options) {
		opts.onConnected = f
	}
}

func WithPrefix(prefix string) Option {
	return func(opts *Options) {
		opts.prefix = prefix
	}
}

func HiddenURL() Option {
	return func(opts *Options) {
		opts.hideURL = true
	}
}

func DisableReconnect() Option {
	return func(opts *Options) {
		opts.disableReconnect = true
	}
}
