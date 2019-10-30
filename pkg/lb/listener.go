package lb

import (
	"crypto/tls"
	"errors"
	"net"
	"sync"

	"github.com/goinsane/accepter"
	"github.com/goinsane/xlog"
)

// ListenerOptions holds Listener options
type ListenerOptions struct {
	Name      string
	Network   string
	Address   string
	Fe        Frontend
	TLSConfig *tls.Config
}

// CopyFrom sets the underlying ListenerOptions by given ListenerOptions
func (o *ListenerOptions) CopyFrom(src *ListenerOptions) {
	*o = *src
}

// Listener implements a network listener
type Listener struct {
	opts   ListenerOptions
	accr   *accepter.Accepter
	accrMu sync.RWMutex
}

// NewListener creates a new Listener by given options
func NewListener(opts ListenerOptions) (l *Listener, err error) {
	l, err = l.Fork(opts)
	return
}

// Fork forkes a Listener and its own members by given options
func (l *Listener) Fork(opts ListenerOptions) (ln *Listener, err error) {
	ln = &Listener{}
	ln.opts.CopyFrom(&opts)

	defer func() {
		if err == nil {
			return
		}
		ln.Close()
		ln = nil
	}()

	if l != nil {
		l.accrMu.RLock()
		defer l.accrMu.RUnlock()
		if l.accr != nil && l.accr.Handler.(*accepterHandler).SetShared(true) {
			err = errors.New("already forked")
			return
		}
		if ln.opts.Network != l.opts.Network {
			err = errors.New("network different from old one")
			return
		}
		if ln.opts.Address != l.opts.Address {
			err = errors.New("address different from old one")
			return
		}
		ln.accr = l.accr
		return
	}

	var lis net.Listener
	lis, err = net.Listen(ln.opts.Network, ln.opts.Address)
	if err != nil {
		return
	}
	ln.accr = &accepter.Accepter{
		Handler: &accepterHandler{},
	}

	go func(lis net.Listener, opts ListenerOptions, accr *accepter.Accepter) {
		if e := accr.Serve(lis); e != nil {
			xlog.Fatalf("listener %q serve error: %v", opts.Name, e)
		}
	}(lis, ln.opts, ln.accr)

	return
}

// Close closes the Listener and its own members
func (l *Listener) Close() {
	l.accrMu.Lock()
	if l.accr != nil {
		if !l.accr.Handler.(*accepterHandler).SetShared(false) {
			l.accr.Close()
		}
		l.accr = nil
	}
	l.accrMu.Unlock()
	return
}

// GetOpts returns a copy of underlying Listener's options
func (l *Listener) GetOpts() (opts ListenerOptions) {
	opts.CopyFrom(&l.opts)
	return
}

// Activate activates Listener after Fork
func (l *Listener) Activate() {
	l.accrMu.RLock()
	if l.accr != nil {
		l.accr.Handler.(*accepterHandler).Set(l, l.opts.Fe, l.opts.TLSConfig)
	}
	l.accrMu.RUnlock()
}
