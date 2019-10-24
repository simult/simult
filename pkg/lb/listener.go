package lb

import (
	"crypto/tls"
	"errors"
	"net"
	"sync"

	"github.com/goinsane/accepter"
)

type ListenerOptions struct {
	Name      string
	Network   string
	Address   string
	Fe        Frontend
	TLSConfig *tls.Config
}

func (o *ListenerOptions) CopyFrom(src *ListenerOptions) {
	*o = *src
}

type Listener struct {
	opts   ListenerOptions
	accr   *accepter.Accepter
	accrMu sync.RWMutex
}

func NewListener(opts ListenerOptions) (l *Listener, err error) {
	l, err = l.Fork(opts)
	return
}

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
			errorLogger.Printf("listener %q serve error: %v", opts.Name, e)
		}
	}(lis, ln.opts, ln.accr)

	return
}

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

func (l *Listener) GetOpts() (opts ListenerOptions) {
	opts.CopyFrom(&l.opts)
	return
}

func (l *Listener) Activate() {
	l.accrMu.RLock()
	if l.accr != nil {
		l.accr.Handler.(*accepterHandler).Set(l, l.opts.Fe, l.opts.TLSConfig)
	}
	l.accrMu.RUnlock()
}
